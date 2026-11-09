package agents

import (
	"cmp"
	"encoding/json"
	"strings"
)

// Hook is a hook event as Hammock reads it, whichever agent sent it: its
// event under Hammock's name (Claude Code's: SessionStart, UserPromptSubmit,
// PreToolUse, PostToolUse, PermissionRequest, Notification, Stop,
// StopFailure, SessionEnd) and what it says.
type Hook struct {
	Event            string
	SessionID        string
	TranscriptPath   string
	Prompt           string
	ToolName         string
	ToolInput        json.RawMessage
	Message          string
	NotificationType string // Claude's: permission_prompt, idle_prompt
	LastMessage      string // its reply, on Stop
	Error            string // StopFailure's kind: rate_limit, server_error…
	ErrorDetails     string
}

// ReadHook reads an event from a: with its own ParseHook, else generically.
func (a *Agent) ReadHook(event string, payload []byte) Hook {
	if a != nil && a.ParseHook != nil {
		return a.ParseHook(event, payload)
	}
	var events map[string]string
	if a != nil {
		events = a.HookEvents
	}
	return ParseHook(event, payload, events)
}

// ParseHook reads a hook payload in the shape most agents send (Claude
// Code's, which Codex, Droid, Qwen, Copilot and others follow, in snake or
// camel case), mapping its event's name with events (nil: as it is).
func ParseHook(event string, payload []byte, events map[string]string) Hook {
	var p struct {
		HookEventName  string          `json:"hook_event_name"`
		HookEventCamel string          `json:"hookEventName"`
		Type           string          `json:"type"` // codex notify
		SessionID      string          `json:"session_id"`
		SessionCamel   string          `json:"sessionId"`
		ConversationID string          `json:"conversation_id"`
		ThreadID       string          `json:"thread-id"` // codex notify
		TranscriptPath string          `json:"transcript_path"`
		TranscriptCaml string          `json:"transcriptPath"`
		Prompt         string          `json:"prompt"`
		UserPrompt     string          `json:"user_prompt"`
		ToolName       string          `json:"tool_name"`
		ToolNameCamel  string          `json:"toolName"`
		ToolInput      json.RawMessage `json:"tool_input"`
		ToolInputCamel json.RawMessage `json:"toolInput"`
		ToolArgs       json.RawMessage `json:"toolArgs"` // Copilot's (a JSON string)
		Message        string          `json:"message"`
		NotifType      string          `json:"notification_type"`
		NotifTypeCamel string          `json:"notificationType"`
		LastMessage    string          `json:"last-assistant-message"` // codex notify
		ClaudeLast     string          `json:"last_assistant_message"` // Claude's Stop, StopFailure
		Error          string          `json:"error"`
		ErrorDetails   string          `json:"error_details"`
	}
	_ = json.Unmarshal(payload, &p)
	name := cmp.Or(event, p.HookEventName, p.HookEventCamel, p.Type)
	if events != nil {
		if mapped, ok := events[name]; ok {
			name = mapped
		}
	}
	input := rawOr(p.ToolInput)
	if input == nil {
		input = rawOr(p.ToolInputCamel)
	}
	if input == nil && len(p.ToolArgs) > 0 {
		var s string
		if json.Unmarshal(p.ToolArgs, &s) == nil && json.Valid([]byte(s)) {
			input = json.RawMessage(s)
		} else {
			input = p.ToolArgs
		}
	}
	return Hook{
		Event:            name,
		SessionID:        cmp.Or(p.SessionID, p.SessionCamel, p.ConversationID, p.ThreadID),
		TranscriptPath:   cmp.Or(p.TranscriptPath, p.TranscriptCaml),
		Prompt:           cmp.Or(p.Prompt, p.UserPrompt),
		ToolName:         cmp.Or(p.ToolName, p.ToolNameCamel),
		ToolInput:        input,
		Message:          p.Message,
		NotificationType: cmp.Or(p.NotifType, p.NotifTypeCamel),
		LastMessage:      cmp.Or(p.ClaudeLast, p.LastMessage),
		Error:            p.Error,
		ErrorDetails:     p.ErrorDetails,
	}
}

// rawOr is raw, or nil when it's empty or JSON null.
func rawOr(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "null" {
		return nil
	}
	return raw
}
