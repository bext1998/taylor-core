package pirpc

import (
	"reflect"
	"testing"
)

// TestDecodeRPCEvent covers the wire-format contract of issue #9 §2: every
// event type Pi may emit is decoded into the flat Event the run loop
// drives, and events Brunel does not surface (session header, unknown
// types, malformed lines) return (zero, false).
func TestDecodeRPCEvent(t *testing.T) {
	tests := []struct {
		name string
		line string
		want Event
		ok   bool
	}{
		{
			name: "session header is not surfaced",
			line: `{"type":"session","session_id":"abc"}`,
			want: Event{Type: "session"},
			ok:   false,
		},
		{
			name: "empty line is skipped",
			line: `   `,
			want: Event{},
			ok:   false,
		},
		{
			name: "malformed line is skipped",
			line: `{"type":"message_update"`,
			want: Event{},
			ok:   false,
		},
		{
			name: "response success ack",
			line: `{"type":"response","success":true,"command":"prompt"}`,
			want: Event{Type: "response", Response: true, Success: true, Command: "prompt"},
			ok:   true,
		},
		{
			name: "response failure ack",
			line: `{"type":"response","success":false,"command":"prompt"}`,
			want: Event{Type: "response", Response: true, Success: false, Command: "prompt"},
			ok:   true,
		},
		{
			name: "text_delta carries usage and delta",
			line: `{"type":"message_update","usage":{"input":10,"output":20,"cacheRead":5,"cacheWrite":0,"totalTokens":35,"cost":{"input":0.001,"output":0.002,"total":0.003}},"assistantMessageEvent":{"type":"text_delta","delta":"Hello world"}}`,
			want: Event{
				Type:          "message_update",
				AssistantType: "text_delta",
				DeltaText:     "Hello world",
				Usage: &Usage{
					Input:       10,
					Output:      20,
					TotalTokens: 35,
					Cost:        floatPtr(0.003),
				},
			},
			ok: true,
		},
		{
			name: "text_delta with no cost leaves cost nil",
			line: `{"type":"message_update","usage":{"input":1,"output":2,"totalTokens":3},"assistantMessageEvent":{"type":"text_delta","delta":"x"}}`,
			want: Event{
				Type:          "message_update",
				AssistantType: "text_delta",
				DeltaText:     "x",
				Usage: &Usage{
					Input:       1,
					Output:      2,
					TotalTokens: 3,
				},
			},
			ok: true,
		},
		{
			name: "toolcall_start carries id and tool name (top-level per protocol)",
			line: `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_start","id":"call_1","toolName":"read_file"}}`,
			want: Event{
				Type:          "message_update",
				AssistantType: "toolcall_start",
				CallID:        "call_1",
				ToolName:      "read_file",
			},
			ok: true,
		},
		{
			name: "toolcall_end carries the completed call nested in toolCall",
			line: `{"type":"message_update","assistantMessageEvent":{"type":"toolcall_end","toolCall":{"id":"call_1","name":"read_file","arguments":{"path":"a.txt"}}}}`,
			want: Event{
				Type:          "message_update",
				AssistantType: "toolcall_end",
				CallID:        "call_1",
				ToolName:      "read_file",
			},
			ok: true,
		},
		{
			name: "tool_execution_start carries toolCallId and toolName",
			line: `{"type":"tool_execution_start","toolCallId":"call_1","toolName":"read_file"}`,
			want: Event{
				Type:       "tool_execution_start",
				ToolCallID: "call_1",
				ExecName:   "read_file",
			},
			ok: true,
		},
		{
			name: "tool_execution_end flags error",
			line: `{"type":"tool_execution_end","toolCallId":"call_1","toolName":"read_file","isError":true}`,
			want: Event{
				Type:       "tool_execution_end",
				ToolCallID: "call_1",
				ExecName:   "read_file",
				IsError:    true,
			},
			ok: true,
		},
		{
			name: "tool_execution_end without error flag",
			line: `{"type":"tool_execution_end","toolCallId":"call_1","toolName":"read_file"}`,
			want: Event{
				Type:       "tool_execution_end",
				ToolCallID: "call_1",
				ExecName:   "read_file",
			},
			ok: true,
		},
		{
			name: "agent_end without retry",
			line: `{"type":"agent_end","willRetry":false}`,
			want: Event{Type: "agent_end"},
			ok:   true,
		},
		{
			name: "agent_end with retry",
			line: `{"type":"agent_end","willRetry":true}`,
			want: Event{Type: "agent_end", WillRetry: true},
			ok:   true,
		},
		{
			name: "agent_settled is the completion signal",
			line: `{"type":"agent_settled"}`,
			want: Event{Type: "agent_settled"},
			ok:   true,
		},
		{
			name: "message_end carries stopReason",
			line: `{"type":"message_end","message":{"role":"assistant","stopReason":"stop"}}`,
			want: Event{Type: "message_end", StopReason: "stop"},
			ok:   true,
		},
		{
			name: "message_end carries error stopReason and message",
			line: `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"rate limit exceeded"}}`,
			want: Event{Type: "message_end", StopReason: "error", ErrorMsg: "rate limit exceeded"},
			ok:   true,
		},
		{
			name: "message_end with no message is surfaced empty",
			line: `{"type":"message_end"}`,
			want: Event{Type: "message_end"},
			ok:   true,
		},
		{
			name: "turn_start is surfaced",
			line: `{"type":"turn_start"}`,
			want: Event{Type: "turn_start"},
			ok:   true,
		},
		{
			name: "queue_update is not surfaced",
			line: `{"type":"queue_update","position":3}`,
			want: Event{},
			ok:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := decodeRPCEvent([]byte(tc.line))
			if ok != tc.ok {
				t.Fatalf("decodeRPCEvent ok = %v, want %v", ok, tc.ok)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("decodeRPCEvent = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// TestDecodeRPCEventUnwrapsStringAssistant covers the fallback where Pi
// wraps the assistantMessageEvent payload in a JSON string.
func TestDecodeRPCEventUnwrapsStringAssistant(t *testing.T) {
	line := `{"type":"message_update","assistantMessageEvent":"{\"type\":\"text_delta\",\"delta\":\"wrapped\"}"}`
	got, ok := decodeRPCEvent([]byte(line))
	if !ok {
		t.Fatal("decodeRPCEvent returned not-ok")
	}
	if got.AssistantType != "text_delta" || got.DeltaText != "wrapped" {
		t.Fatalf("decoded assistant = %+v, want text_delta/wrapped", got)
	}
}

// TestMergeEnv covers the environment merge used to inject BRUNEL_EXE /
// BRUNEL_MODE without clobbering inherited variables.
func TestMergeEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/home/user", "OPENROUTER_API_KEY=abc"}

	t.Run("empty add returns base unchanged", func(t *testing.T) {
		got := mergeEnv(base, nil)
		if !reflect.DeepEqual(got, base) {
			t.Fatalf("mergeEnv(nil) = %v, want %v", got, base)
		}
	})

	t.Run("adds a new key", func(t *testing.T) {
		got := mergeEnv(base, map[string]string{"BRUNEL_EXE": "/x/brunel.exe"})
		found := false
		for _, e := range got {
			if e == "BRUNEL_EXE=/x/brunel.exe" {
				found = true
			}
		}
		if !found {
			t.Fatalf("mergeEnv did not add BRUNEL_EXE: %v", got)
		}
	})

	t.Run("replaces an existing key case-insensitively", func(t *testing.T) {
		got := mergeEnv(base, map[string]string{"OPENROUTER_API_KEY": "def"})
		found := false
		for _, e := range got {
			if e == "OPENROUTER_API_KEY=def" {
				found = true
			}
		}
		if !found {
			t.Fatalf("mergeEnv did not replace OPENROUTER_API_KEY: %v", got)
		}
	})
}

func floatPtr(f float64) *float64 { return &f }
