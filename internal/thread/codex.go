package thread

// Codex rollout codec. Format and resume rules follow Atuin's implementation
// (github.com/atuinsh/atuin, crates/atuin-common/src/harnesstools/codex, MIT):
// $CODEX_HOME/sessions/YYYY/MM/DD/rollout-<UTC start>-<id>.jsonl, each line
// {"timestamp","type","payload"}. Codex 0.156+ rebuilds the visible history
// of a "paginated" rollout from turn/item events, so for those versions the
// writer numbers lines and synthesizes task_started / item_completed /
// task_complete events; older versions get a plain rollout of response items.

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Codex reads and writes Codex rollouts.
type Codex struct {
	// Home overrides $CODEX_HOME (default ~/.codex).
	Home string
	// Paginated forces the rollout mode; nil detects it from `codex --version`.
	Paginated *bool
}

func (Codex) Harness() string { return "codex" }

func (c Codex) home() string {
	if c.Home != "" {
		return c.Home
	}
	if d := os.Getenv("CODEX_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

type cxLine struct {
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

type cxItem struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Cwd       string          `json:"cwd"`
	Role      string          `json:"role"`
	Content   []cxContent     `json:"content"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Input     string          `json:"input"`
	Output    json.RawMessage `json:"output"`
	Action    json.RawMessage `json:"action"`
}

type cxContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// contextual markers Codex wraps injected (non-user) context in.
var codexContext = regexp.MustCompile(`^\s*(<(environment_context|user_instructions|user_shell_command|turn_aborted|subagent_notification)\b|# AGENTS\.md instructions\b)`)

// CodexInjected reports whether a user message in a rollout is context
// Codex put there (AGENTS.md, its environment), not something you typed.
func CodexInjected(text string) bool { return codexContext.MatchString(text) }

// Decode reads a rollout's response items into neutral records.
func (c Codex) Decode(r io.Reader) (Thread, []Record, error) {
	t := Thread{Harness: "codex"}
	var recs []Record
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var l cxLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, l.Timestamp)
		var it cxItem
		if json.Unmarshal(l.Payload, &it) != nil {
			continue
		}
		switch l.Type {
		case "session_meta":
			t.NativeID = it.ID
			if t.NativeID == "" {
				t.NativeID = it.SessionID
			}
			t.Cwd, t.Created = it.Cwd, at
			continue
		case "response_item":
		default:
			continue
		}
		switch it.Type {
		case "message":
			var parts []string
			for _, ct := range it.Content {
				if ct.Text != "" {
					parts = append(parts, ct.Text)
				}
			}
			text := strings.Join(parts, "\n")
			switch {
			case strings.TrimSpace(text) == "":
			case it.Role == "user" && !codexContext.MatchString(text):
				recs = append(recs, Record{Kind: KindUser, Time: at, Text: text})
			case it.Role == "assistant":
				recs = append(recs, Record{Kind: KindAssistant, Time: at, Text: text})
			}
		case "reasoning":
			recs = append(recs, Record{Kind: KindReasoning, Time: at})
		case "function_call":
			recs = append(recs, codexFunctionCall(it, at))
		case "local_shell_call":
			var a struct {
				Command []string `json:"command"`
			}
			_ = json.Unmarshal(it.Action, &a)
			recs = append(recs, Record{Kind: KindToolCall, Time: at, CallID: it.CallID, NativeTool: "local_shell", Tool: ToolShell,
				Input: mustJSON(map[string]string{"command": unwrapShell(a.Command)})})
		case "custom_tool_call":
			r := Record{Kind: KindToolCall, Time: at, CallID: it.CallID, NativeTool: it.Name, Input: mustJSON(map[string]string{"input": Clip(it.Input)})}
			switch it.Name {
			case "apply_patch":
				if p := patchPaths(it.Input); len(p) > 0 {
					r.Edit = &Edit{Path: p[0]}
				}
			case "exec":
				codexExec(&r, it.Input)
			}
			recs = append(recs, r)
		case "function_call_output", "custom_tool_call_output":
			recs = append(recs, Record{Kind: KindToolResult, Time: at, CallID: it.CallID, Text: Clip(codexOutput(it.Output))})
		}
	}
	if t.Title == "" && t.NativeID != "" {
		// Its name, if it has one (Codex keeps them in its own list).
		if n := CodexNames(c.home())[t.NativeID]; n != "" {
			t.Title, t.Named = n, true
		}
	}
	return t, recs, sc.Err()
}

// CodexNames reads Codex's names for its sessions (session_index.jsonl in
// its home, with renames appended later), by session ID.
func CodexNames(home string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(filepath.Join(home, "session_index.jsonl"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" && e.Name != "" {
			out[e.ID] = e.Name
		}
	}
	return out
}

func codexFunctionCall(it cxItem, at time.Time) Record {
	r := Record{Kind: KindToolCall, Time: at, CallID: it.CallID, NativeTool: it.Name, Input: clipJSON(json.RawMessage(it.Arguments))}
	if !json.Valid([]byte(it.Arguments)) {
		r.Input = mustJSON(map[string]string{"input": Clip(it.Arguments)})
	}
	var args struct {
		Cmd     json.RawMessage `json:"cmd"`
		Command json.RawMessage `json:"command"`
	}
	_ = json.Unmarshal([]byte(it.Arguments), &args)
	switch it.Name {
	case "exec_command", "shell", "shell_command", "local_shell":
		raw := args.Cmd
		if len(raw) == 0 {
			raw = args.Command
		}
		var s string
		var argv []string
		cmd := ""
		if json.Unmarshal(raw, &s) == nil {
			cmd = s
		} else if json.Unmarshal(raw, &argv) == nil {
			cmd = unwrapShell(argv)
		}
		r.Tool, r.Input = ToolShell, mustJSON(map[string]string{"command": cmd})
	}
	return r
}

// Codex (0.16x) calls its tools from a small script run by its "exec" tool:
// tools.exec_command({cmd: "…"}) for a command, a "*** Begin Patch" for an
// edit. As those, a converted session shows (and Claude sees) a command or
// an edit, not an unknown tool.
var execCommandRE = regexp.MustCompile(`exec_command\(\{\s*"?cmd"?\s*:\s*("(?:[^"\\]|\\.)*")`)

func codexExec(r *Record, script string) {
	if strings.Contains(script, "*** Begin Patch") {
		patch := strings.NewReplacer(`\n`, "\n", `\"`, `"`, `\\`, `\`).Replace(script)
		if p := patchPaths(patch); len(p) > 0 {
			r.NativeTool, r.Edit = "apply_patch", &Edit{Path: p[0]}
			if i, j := strings.Index(patch, "*** Begin Patch"), strings.Index(patch, "*** End Patch"); i >= 0 && j > i {
				r.Input = mustJSON(map[string]string{"input": patch[i : j+len("*** End Patch")]})
			}
		}
		return
	}
	var cmds []string
	for _, m := range execCommandRE.FindAllStringSubmatch(script, -1) {
		if c, err := strconv.Unquote(m[1]); err == nil {
			cmds = append(cmds, c)
		}
	}
	if len(cmds) > 0 {
		r.NativeTool, r.Tool = "exec_command", ToolShell
		r.Input = mustJSON(map[string]string{"command": strings.Join(cmds, "\n")})
	}
}

// unwrapShell turns ["bash","-lc","X"] into X and other argv into a command line.
func unwrapShell(argv []string) string {
	if len(argv) == 3 && (argv[1] == "-lc" || argv[1] == "-c") && strings.HasSuffix(argv[0], "sh") {
		return argv[2]
	}
	return strings.Join(argv, " ")
}

// codexOutput reads an output that may be a string, a {"output":…} object, or content items.
func codexOutput(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		var obj struct {
			Output string `json:"output"`
		}
		if strings.HasPrefix(strings.TrimSpace(s), "{") && json.Unmarshal([]byte(s), &obj) == nil && obj.Output != "" {
			return obj.Output
		}
		return s
	}
	var items []cxContent
	if json.Unmarshal(raw, &items) == nil {
		var parts []string
		for i, it := range items {
			// An exec script's own header ("Script completed / Wall time …
			// / Output:"): what the command printed follows it.
			if i == 0 && len(items) > 1 && strings.HasPrefix(it.Text, "Script completed") && strings.HasSuffix(strings.TrimSpace(it.Text), "Output:") {
				continue
			}
			parts = append(parts, it.Text)
		}
		return strings.Join(parts, "\n")
	}
	return string(raw)
}

var patchFile = regexp.MustCompile(`(?m)^\*\*\* (?:Update|Add|Delete) File: (.+)$`)

func patchPaths(patch string) []string {
	var out []string
	for _, m := range patchFile.FindAllStringSubmatch(patch, -1) {
		out = append(out, strings.TrimSpace(m[1]))
	}
	return out
}

// codexNative are Codex's own tools; foreign tools with these names are prefixed.
var codexNative = map[string]bool{
	"exec_command": true, "shell": true, "shell_command": true, "apply_patch": true, "update_plan": true,
	"view_image": true, "web_search": true, "write_stdin": true,
}

// Encode writes records as a new Codex rollout.
func (c Codex) Encode(t Thread, recs []Record) (string, string, []byte, error) {
	if t.Cwd == "" {
		return "", "", nil, fmt.Errorf("codex: session needs a working directory")
	}
	paginated := codexPaginated()
	if c.Paginated != nil {
		paginated = *c.Paginated
	}
	id := NewID()
	start := time.Now().UTC().Add(-time.Duration(len(recs)*2+10) * time.Millisecond)
	w := &cxWriter{at: start, paginated: paginated}
	meta := map[string]any{
		"session_id": id, "id": id, "timestamp": stampCodex(start), "cwd": t.Cwd,
		"originator": "codex_cli_rs", "cli_version": "0.0.0", "source": "cli", "model_provider": c.provider(),
	}
	if paginated {
		meta["history_mode"] = "paginated"
	}
	if t.Title != "" {
		meta["thread_name"] = t.Title // its name in Codex's resume list
	}
	w.line("session_meta", meta)
	w.item(map[string]any{"type": "message", "role": "developer", "content": []any{map[string]any{"type": "input_text", "text": continuationMarker(t, recs)}}})

	turn := ""
	turnStart := w.at
	lastAnswer := ""
	pending := map[string]bool{}
	var order []string
	used := map[string]int{}
	ids := map[string]string{}
	closeCalls := func() {
		for _, cid := range order {
			if pending[cid] {
				w.item(map[string]any{"type": "function_call_output", "call_id": cid, "output": "[no result was recorded]"})
			}
		}
		pending, order = map[string]bool{}, nil
	}
	endTurn := func() {
		closeCalls()
		if paginated && turn != "" {
			w.line("event_msg", map[string]any{"type": "task_complete", "turn_id": turn, "last_agent_message": nilIfEmpty(lastAnswer),
				"started_at": turnStart.Unix(), "completed_at": w.at.Unix()})
		}
		turn, lastAnswer = "", ""
	}
	beginTurn := func(prompt string) {
		endTurn()
		turn = deterministicUUID(id + "\x00" + strconv.Itoa(w.n))
		turnStart = w.at
		if paginated {
			w.line("event_msg", map[string]any{"type": "task_started", "turn_id": turn, "root_turn_id": turn, "started_at": turnStart.Unix(), "model_context_window": nil})
		}
		w.item(map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": prompt}}})
		if paginated {
			w.itemCompleted(id, turn, map[string]any{"type": "UserMessage", "id": uuid4(), "content": []any{map[string]any{"type": "text", "text": prompt, "text_elements": []any{}}}})
		}
	}
	for _, r := range recs {
		switch r.Kind {
		case KindUser:
			beginTurn(r.Text)
		case KindAssistant, KindNote:
			if turn == "" {
				beginTurn("[continuing an earlier session]")
			}
			text := r.Text
			if r.Kind == KindNote {
				text = "[" + text + "]"
			}
			if paginated {
				w.itemCompleted(id, turn, map[string]any{"type": "AgentMessage", "id": uuid4(), "content": []any{map[string]any{"type": "Text", "text": text}}})
			}
			w.item(map[string]any{"type": "message", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": text}}})
			lastAnswer = text
		case KindToolCall:
			if turn == "" {
				beginTurn("[continuing an earlier session]")
			}
			cid := uniqueID(sanitize(r.CallID, "call"), used)
			ids[r.CallID] = cid
			name, args := codexTool(r, t)
			w.item(map[string]any{"type": "function_call", "call_id": cid, "name": name, "arguments": args})
			pending[cid] = true
			order = append(order, cid)
		case KindToolResult:
			cid, ok := ids[r.CallID]
			if !ok || !pending[cid] {
				continue
			}
			delete(pending, cid)
			out := r.Text
			if r.IsError {
				out = "[error] " + out
			}
			w.item(map[string]any{"type": "function_call_output", "call_id": cid, "output": out})
		}
	}
	endTurn()
	path := filepath.Join(c.home(), "sessions", start.Format("2006"), start.Format("01"), start.Format("02"),
		"rollout-"+start.Format("2006-01-02T15-04-05")+"-"+id+".jsonl")
	return id, path, w.buf.Bytes(), nil
}

func (Codex) ResumeCommand(id string) []string { return []string{"codex", "resume", id} }

// codexTool maps a record to a Codex function name and JSON-string arguments.
func codexTool(r Record, t Thread) (string, string) {
	if r.Tool == ToolShell {
		var in struct {
			Command string `json:"command"`
		}
		_ = json.Unmarshal(r.Input, &in)
		return "exec_command", string(mustJSON(map[string]string{"cmd": in.Command, "workdir": t.Cwd}))
	}
	name := sanitize(r.NativeTool, "tool")
	if codexNative[name] && t.Source != "codex" {
		name = sanitize(orDefault(t.Source, "other")+"_"+name, "tool")
	}
	in := r.Input
	if r.Edit != nil && (r.Tool == ToolWrite || r.Tool == ToolEdit) {
		in = mustJSON(map[string]string{"file_path": r.Edit.Path, "old_string": r.Edit.Old, "new_string": r.Edit.New})
	}
	var obj map[string]any
	if json.Unmarshal(in, &obj) != nil || obj == nil {
		in = mustJSON(map[string]string{"input": string(in)})
	}
	return name, string(in)
}

// provider reads model_provider from $CODEX_HOME/config.toml (default "openai"):
// a rollout naming another provider is hidden from `codex resume`.
func (c Codex) provider() string {
	b, err := os.ReadFile(filepath.Join(c.home(), "config.toml"))
	if err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				break // top-level keys only
			}
			if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "model_provider" {
				if p := strings.Trim(strings.TrimSpace(v), `"'`); p != "" {
					return p
				}
			}
		}
	}
	return "openai"
}

var codexVersionRE = regexp.MustCompile(`(\d+)\.(\d+)\.(\d+)`)

// codexPaginated reports whether the installed Codex (0.156+) expects paginated rollouts.
func codexPaginated() bool {
	out, err := exec.Command("codex", "--version").Output()
	if err != nil {
		return true
	}
	m := codexVersionRE.FindStringSubmatch(string(out))
	if m == nil {
		return true
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	return major > 0 || minor >= 156
}

type cxWriter struct {
	buf       bytes.Buffer
	at        time.Time
	n         int
	paginated bool
}

func (w *cxWriter) line(typ string, payload any) {
	w.at = w.at.Add(time.Millisecond)
	l := map[string]any{"timestamp": stampCodex(w.at), "type": typ, "payload": payload}
	if w.paginated {
		l["ordinal"] = w.n
	}
	w.n++
	b, _ := json.Marshal(l)
	w.buf.Write(b)
	w.buf.WriteByte('\n')
}

func (w *cxWriter) item(payload any) { w.line("response_item", payload) }

func (w *cxWriter) itemCompleted(thread, turn string, item any) {
	ms := w.at.UnixMilli()
	w.line("event_msg", map[string]any{"type": "item_completed", "thread_id": thread, "turn_id": turn, "item": item, "started_at_ms": ms, "completed_at_ms": ms})
}

func stampCodex(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// deterministicUUID derives a stable UUID-shaped id from s.
func deterministicUUID(s string) string {
	sum := sha256.Sum256([]byte(s))
	b := sum[:16]
	b[6] = (b[6] & 0x0F) | 0x50
	b[8] = (b[8] & 0x3F) | 0x80
	h := hex.EncodeToString(b)
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

func init() {
	Register(Claude{})
	Register(Codex{})
}

// Newest is the most recent session begun in cwd (its rollout's
// session_meta), for a Codex whose session isn't known (no hooks). It looks
// at the latest few only.
func (c Codex) Newest(cwd string) (string, bool) {
	var m []string
	for _, ext := range []string{".jsonl", ".jsonl.zst"} {
		g, _ := filepath.Glob(filepath.Join(c.home(), "sessions", "*", "*", "*", "rollout-*"+ext))
		m = append(m, g...)
	}
	slices.SortFunc(m, func(a, b string) int { return strings.Compare(filepath.Base(b), filepath.Base(a)) })
	for i, p := range m {
		if i == 20 {
			break
		}
		r, err := OpenTranscript(p)
		if err != nil {
			continue
		}
		line, _ := bufio.NewReaderSize(r, 64<<10).ReadBytes('\n')
		r.Close()
		var l struct {
			Type    string `json:"type"`
			Payload struct {
				Cwd string `json:"cwd"`
			} `json:"payload"`
		}
		if json.Unmarshal(line, &l) == nil && l.Type == "session_meta" && filepath.Clean(l.Payload.Cwd) == filepath.Clean(cwd) {
			return p, true
		}
	}
	return "", false
}

// Locate finds a thread's rollout under sessions/ (archived ones can't be
// resumed): .jsonl, or .jsonl.zst as Codex now writes them (OpenTranscript
// reads either).
func (c Codex) Locate(id string) (string, bool) {
	var m []string
	for _, ext := range []string{".jsonl", ".jsonl.zst"} {
		g, _ := filepath.Glob(filepath.Join(c.home(), "sessions", "*", "*", "*", "rollout-*-"+id+ext))
		m = append(m, g...)
	}
	if len(m) == 0 {
		return "", false
	}
	slices.Sort(m) // by date: the newest
	return m[len(m)-1], true
}
