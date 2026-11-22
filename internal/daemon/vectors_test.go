package daemon

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/homegrownsw/hammock/internal/client"
	pb "github.com/homegrownsw/hammock/internal/proto/hammockv1"
	"google.golang.org/protobuf/proto"
)

// clientVectors are what the phone apps do that the daemon checks, the same
// in Swift (the iPhone) and Kotlin (Android) as here: what a device signs to
// sign in, a pairing link, a sealed notification, what a phone signs to
// approve the Hammock Key. testdata/client/vectors.json holds them; the
// iPhone's (apple/Tests/HammockKitTests/VectorsTests.swift) and Android's
// (android/client/src/test/…/VectorsTest.kt) tests read the same file, so a
// change to one side that the others don't follow fails somewhere.
// HMK_TEST_UPDATE_VECTORS=1 writes it afresh (a push note's nonce is random).
type clientVectors struct {
	Auth []struct {
		Cert    string `json:"cert"`
		Nonce   string `json:"nonce_hex"`
		Message string `json:"message_hex"`
	} `json:"auth"`
	Pairing []struct {
		Link          string   `json:"link"`
		HostName      string   `json:"host_name"`
		Addresses     []string `json:"addresses"`
		LanSubnets    []string `json:"lan_subnets"`
		CertSHA256    string   `json:"cert_sha256"`
		Secret        string   `json:"secret_hex"`
		RemoteEnabled bool     `json:"remote_enabled"`
	} `json:"pairing"`
	Push []struct {
		Host   string          `json:"host"`
		Key    string          `json:"key_hex"`
		Sealed string          `json:"sealed_b64"`
		Note   json.RawMessage `json:"note"`
	} `json:"push"`
	Approval []struct {
		KeyID    string `json:"key_id"`
		ID       string `json:"id"`
		DataHash string `json:"data_hash"`
		PaneID   string `json:"pane_id"`
		Window   bool   `json:"window"`
		Expires  int64  `json:"expires_unix_ms"`
		Message  string `json:"message"`
	} `json:"approval"`
	Notice []noticeVector `json:"notice"`
	Names  []nameVector   `json:"names"`
	Badge  []badgeVector  `json:"badge"`
	// A Live Activity's content as the daemon sends it (live.go), for the
	// phones to read the same way.
	Live []json.RawMessage `json:"live"`
}

// noticeVector is what a notification about an agent says (noticeBody):
// the Mac's own, and the phones' pushes.
type noticeVector struct {
	State    string `json:"state"` // vectorStates' names
	Question bool   `json:"question"`
	Program  bool   `json:"program"`
	Summary  string `json:"summary"`
	Who      string `json:"who"`
	Body     string `json:"body"`
}

// nameVector is a pane's name as a notification names it
// (capitalizeWords, Foundation's capitalized).
type nameVector struct {
	Name string `json:"name"`
	Who  string `json:"who"`
}

// badgeVector is the number on Hammock's icon (badgeCount) for what needs
// you: attention items by state, and how many permission, join and Hammock
// Key requests.
type badgeVector struct {
	Attention []string `json:"attention"`
	Grants    int      `json:"grants"`
	Joins     int      `json:"joins"`
	Keys      int      `json:"keys"`
	Count     int      `json:"count"`
}

var vectorStates = map[string]pb.AgentState{
	"needs_input": pb.AgentState_AGENT_STATE_NEEDS_INPUT, "idle": pb.AgentState_AGENT_STATE_IDLE,
	"errored": pb.AgentState_AGENT_STATE_ERRORED, "working": pb.AgentState_AGENT_STATE_WORKING,
}

// badgeDaemon holds what a badgeVector counts.
func badgeDaemon(v badgeVector) *Daemon {
	d := &Daemon{attention: map[string]*pb.AttentionItem{}, grants: map[string]*pendingGrant{}, joins: map[string]*pendingJoin{},
		hammockKey: &hammockKeyHub{reqs: map[string]*keyReq{}}}
	for i, s := range v.Attention {
		d.attention[fmt.Sprint("at_", i)] = &pb.AttentionItem{State: vectorStates[s]}
	}
	for i := range v.Grants {
		d.grants[fmt.Sprint("gr_", i)] = &pendingGrant{}
	}
	for i := range v.Joins {
		d.joins[fmt.Sprint("jn_", i)] = &pendingJoin{}
	}
	for i := range v.Keys {
		d.hammockKey.reqs[fmt.Sprint("hk_", i)] = &keyReq{}
	}
	return d
}

var vectorsPath = filepath.Join("..", "..", "testdata", "client", "vectors.json")

func TestClientVectors(t *testing.T) {
	if os.Getenv("HMK_TEST_UPDATE_VECTORS") == "1" {
		writeClientVectors(t)
	}
	b, err := os.ReadFile(vectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	var v clientVectors
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Auth) == 0 || len(v.Pairing) == 0 || len(v.Push) == 0 || len(v.Approval) == 0 || len(v.Notice) == 0 || len(v.Names) == 0 || len(v.Badge) == 0 || len(v.Live) == 0 {
		t.Fatal("a kind of vector is missing")
	}
	for _, n := range v.Notice {
		item := &pb.AttentionItem{State: vectorStates[n.State], Question: n.Question, Program: n.Program, Summary: n.Summary}
		if got := noticeBody(item, n.Who); got != n.Body {
			t.Errorf("notice %+v says %q", n, got)
		}
	}
	for _, n := range v.Names {
		if got := capitalizeWords(n.Name); got != n.Who {
			t.Errorf("%q named %q, want %q", n.Name, got, n.Who)
		}
	}
	for _, raw := range v.Live {
		var c liveContent
		if err := json.Unmarshal(raw, &c); err != nil || string(mustJSON(c)) != string(mustJSON(json.RawMessage(raw))) || c.Status != liveStatus(c.State) {
			t.Errorf("live content %s reads as %+v (%v)", raw, c, err)
		}
	}
	for _, b := range v.Badge {
		if got := badgeDaemon(b).badgeCount(); got != b.Count {
			t.Errorf("badge %+v counts %d", b, got)
		}
	}
	for _, a := range v.Auth {
		nonce, _ := hex.DecodeString(a.Nonce)
		if got := hex.EncodeToString(client.AuthMessage(client.DeviceAuthTag, a.Cert, nonce)); got != a.Message {
			t.Errorf("auth message for %s: %s, want %s", a.Cert, got, a.Message)
		}
	}
	for _, p := range v.Pairing {
		raw, err := base64.RawURLEncoding.DecodeString(p.Link[len("hammock://pair/"):])
		if err != nil {
			t.Fatal(err)
		}
		var got pb.Pairing
		if err := proto.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		if got.HostName != p.HostName || got.CertSha256 != p.CertSHA256 || hex.EncodeToString(got.Secret) != p.Secret ||
			len(got.Addresses) != len(p.Addresses) || got.RemoteEnabled != p.RemoteEnabled {
			t.Errorf("pairing %s decodes to %v", p.Link, &got)
		}
	}
	for _, p := range v.Push {
		key, _ := hex.DecodeString(p.Key)
		sealed, _ := base64.StdEncoding.DecodeString(p.Sealed)
		block, _ := aes.NewCipher(key)
		gcm, _ := cipher.NewGCM(block)
		plain, err := gcm.Open(nil, sealed[:12], sealed[12:], nil)
		if err != nil {
			t.Fatalf("push note for %s: %v", p.Host, err)
		}
		var a, b any
		_ = json.Unmarshal(plain, &a)
		_ = json.Unmarshal(p.Note, &b)
		if ja, _ := json.Marshal(a); string(ja) != string(mustJSON(b)) {
			t.Errorf("push note opens to %s, want %s", plain, p.Note)
		}
	}
	for _, a := range v.Approval {
		scope := "once"
		if a.Window {
			scope = "window"
		}
		if got := string(approvalMessage(a.KeyID, a.ID, a.DataHash, scope, a.PaneID, a.Expires)); got != a.Message {
			t.Errorf("approval message %q, want %q", got, a.Message)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

// writeClientVectors makes the file from made-up values (test data only).
func writeClientVectors(t *testing.T) {
	var v clientVectors
	cert := "9f2c4e1a7b3d5c8e0f1a2b3c4d5e6f708192a3b4c5d6e7f8091a2b3c4d5e6f70"
	nonce := make([]byte, 32)
	for i := range nonce {
		nonce[i] = byte(i * 7)
	}
	v.Auth = append(v.Auth, struct {
		Cert    string `json:"cert"`
		Nonce   string `json:"nonce_hex"`
		Message string `json:"message_hex"`
	}{cert, hex.EncodeToString(nonce), hex.EncodeToString(client.AuthMessage(client.DeviceAuthTag, cert, nonce))})
	// The pin is lowercased before it's signed, whatever case it came in.
	upper := "9F2C4E1A7B3D5C8E0F1A2B3C4D5E6F708192A3B4C5D6E7F8091A2B3C4D5E6F70"
	v.Auth = append(v.Auth, struct {
		Cert    string `json:"cert"`
		Nonce   string `json:"nonce_hex"`
		Message string `json:"message_hex"`
	}{upper, hex.EncodeToString(nonce), hex.EncodeToString(client.AuthMessage(client.DeviceAuthTag, upper, nonce))})

	secret := make([]byte, 24)
	for i := range secret {
		secret[i] = byte(200 - i)
	}
	p := &pb.Pairing{HostName: "Studio.local", Addresses: []string{"100.64.0.2:7433", "192.168.1.20:7433"}, LanSubnets: []string{"192.168.1.0/24"},
		CertSha256: cert, Secret: secret, ExpiresUnixMs: 1_800_000_000_000, RemoteEnabled: true}
	raw, _ := proto.Marshal(p)
	v.Pairing = append(v.Pairing, struct {
		Link          string   `json:"link"`
		HostName      string   `json:"host_name"`
		Addresses     []string `json:"addresses"`
		LanSubnets    []string `json:"lan_subnets"`
		CertSHA256    string   `json:"cert_sha256"`
		Secret        string   `json:"secret_hex"`
		RemoteEnabled bool     `json:"remote_enabled"`
	}{"hammock://pair/" + base64.RawURLEncoding.EncodeToString(raw), p.HostName, p.Addresses, p.LanSubnets, cert, hex.EncodeToString(secret), true})

	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	badge := 2
	for _, note := range []pushNote{
		{Title: "Claude needs you", Subtitle: "Web ▸ api", Body: "Allow Bash: npm test?", Category: "grant", Grant: "gr_1",
			Host: cert, ID: "c_1", At: 1_800_000_000, Live: true, Kind: "grant", Badge: &badge},
		// An agent's permission prompt: Allow and Deny answer that prompt.
		{Title: "Map Judder", Subtitle: "🗺️ Atlas", Body: "Claude needs your permission to use Bash", Category: agentPromptCategory,
			Pane: "pn_1", Prompt: "3f9a1c", Host: cert, ID: "at_pn_1", At: 1_800_000_000, Live: true, Kind: "needs_input", Badge: &badge},
	} {
		plain, _ := json.Marshal(note)
		sealed, err := seal(key, plain)
		if err != nil {
			t.Fatal(err)
		}
		v.Push = append(v.Push, struct {
			Host   string          `json:"host"`
			Key    string          `json:"key_hex"`
			Sealed string          `json:"sealed_b64"`
			Note   json.RawMessage `json:"note"`
		}{cert, hex.EncodeToString(key), base64.StdEncoding.EncodeToString(sealed), plain})
	}

	for _, w := range []bool{true, false} {
		scope := "once"
		if w {
			scope = "window"
		}
		v.Approval = append(v.Approval, struct {
			KeyID    string `json:"key_id"`
			ID       string `json:"id"`
			DataHash string `json:"data_hash"`
			PaneID   string `json:"pane_id"`
			Window   bool   `json:"window"`
			Expires  int64  `json:"expires_unix_ms"`
			Message  string `json:"message"`
		}{"k1", "fk_1", "ab", "pn_1", w, 123, string(approvalMessage("k1", "fk_1", "ab", scope, "pn_1", 123))})
	}
	for _, n := range []noticeVector{
		{State: "needs_input", Question: true, Summary: "Which database should I use?", Who: "Claude"},
		{State: "needs_input", Summary: "Allow Bash: npm test?", Who: "Claude"},
		{State: "needs_input", Who: "Codex"},
		{State: "needs_input", Program: true, Summary: "Enter your password", Who: "Brew"},
		{State: "idle", Summary: "Done with the refactor", Who: "Claude"},
		{State: "idle", Who: "Claude"},
		{State: "idle", Program: true, Summary: "Deployed to staging", Who: "Deploy"},
		{State: "idle", Program: true, Who: "Deploy"},
		{State: "errored", Summary: "exited with status 1", Who: "Claude"},
		{State: "errored", Who: "Agent"},
		{State: "working", Summary: "Reading files", Who: "Claude"},
	} {
		n.Body = noticeBody(&pb.AttentionItem{State: vectorStates[n.State], Question: n.Question, Program: n.Program, Summary: n.Summary}, n.Who)
		v.Notice = append(v.Notice, n)
	}
	for _, name := range []string{"claude", "codex", "claude-code", "OpenCode", "gpt-5 x2y", "it's my_app.sh", "ÉCOLE"} {
		v.Names = append(v.Names, nameVector{name, capitalizeWords(name)})
	}
	for _, b := range []badgeVector{
		{Attention: []string{}},
		{Attention: []string{"needs_input", "needs_input", "idle", "errored", "working"}},
		{Attention: []string{"needs_input"}, Grants: 2, Joins: 1, Keys: 1},
		{Attention: []string{"idle", "errored"}, Grants: 1},
	} {
		b.Count = badgeDaemon(b).badgeCount()
		v.Badge = append(v.Badge, b)
	}
	for _, c := range liveVectors() {
		v.Live = append(v.Live, mustJSON(c))
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	if err := os.MkdirAll(filepath.Dir(vectorsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(vectorsPath, append(b, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// liveVectors are made-up Live Activity contents: one at work, one waiting
// on a permission it can be answered from, one finished.
func liveVectors() []liveContent {
	pct := 42
	return []liveContent{
		{Agent: "Claude", Harness: "claude", Tab: "Map Judder", Group: "Atlas", GroupIcon: "🗺", Place: "🗺 Atlas · Map Judder", State: liveWorking,
			Status: liveStatus(liveWorking), Doing: "Editing AppModel.swift", Since: 1_800_000_000, Context: &pct, Pane: "pn_1",
			More: "+2 more working", Others: 2, Waiting: 0, Accent: "#1F7A57", AccentDark: "#3FB88A"},
		{Agent: "Codex", Harness: "codex", Tab: "api", Group: "Web", Place: "Web · api", State: liveNeedsYou, Status: liveStatus(liveNeedsYou),
			Doing: "Allow Bash: npm test?", Since: 1_800_000_100, Pane: "pn_2", Prompt: "pr_1", Item: "at_pn_2", More: "+1 more working", Others: 1, Waiting: 1},
		{Agent: "Claude", State: liveFinished, Status: liveStatus(liveFinished), Doing: "All done.", Since: 1_800_000_200},
	}
}
