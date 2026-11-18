package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func rsEv(data string) string {
	var e struct {
		Type string `json:"type"`
	}
	_ = json.Unmarshal([]byte(data), &e)
	return "data: " + data + "\nevent: " + e.Type
}

func streamResponses(t *testing.T, chunks []string, req Request) (Message, Usage, string, string, []byte, error) {
	t.Helper()
	var body []byte
	srv := sseServer(t, chunks, func(r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("path %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
		body, _ = io.ReadAll(r.Body)
	})
	defer srv.Close()
	var text, reasoning strings.Builder
	msg, u, err := NewResponses(srv.URL, "k").Stream(context.Background(), req, func(d Delta) {
		if d.Kind == DeltaText {
			text.WriteString(d.Text)
		} else {
			reasoning.WriteString(d.Text)
		}
	})
	return msg, u, text.String(), reasoning.String(), body, err
}

func TestResponsesStreamAssemblesReasoningTextAndCalls(t *testing.T) {
	reasoning := `{"type":"reasoning","id":"rs_1","encrypted_content":"ENC","summary":[{"type":"summary_text","text":"plan"}]}`
	chunks := []string{
		rsEv(`{"type":"response.created","response":{"status":"in_progress"}}`),
		rsEv(`{"type":"response.reasoning_summary_part.added","summary_index":0}`),
		rsEv(`{"type":"response.reasoning_summary_text.delta","delta":"pla"}`),
		rsEv(`{"type":"response.reasoning_summary_part.added","summary_index":1}`),
		rsEv(`{"type":"response.reasoning_summary_text.delta","delta":"n"}`),
		rsEv(`{"type":"response.output_item.done","item":` + reasoning + `}`),
		rsEv(`{"type":"response.output_text.delta","delta":"Look"}`),
		rsEv(`{"type":"response.output_text.delta","delta":"ing."}`),
		rsEv(`{"type":"response.output_item.done","item":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Looking."}]}}`),
		rsEv(`{"type":"response.function_call_arguments.delta","delta":"{\"command\""}`),
		rsEv(`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"bash","arguments":"{\"command\":\"ls\"}"}}`),
		rsEv(`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":20,"input_tokens_details":{"cached_tokens":64,"cache_write_tokens":3}}}}`),
	}
	msg, u, text, shown, _, err := streamResponses(t, chunks, Request{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "Looking." || msg.Text() != "Looking." {
		t.Fatalf("text %q / %q", text, msg.Text())
	}
	if shown != "pla\n\nn" {
		t.Fatalf("reasoning shown %q", shown)
	}
	if len(msg.Blocks) != 3 || msg.Blocks[0].Type != BlockReasoning || msg.Blocks[0].Model != "m" || string(msg.Blocks[0].Raw) != reasoning {
		t.Fatalf("blocks %+v", msg.Blocks)
	}
	calls := msg.ToolUses()
	if len(calls) != 1 || calls[0].ID != "call_1" || calls[0].Name != "bash" || string(calls[0].Input) != `{"command":"ls"}` {
		t.Fatalf("calls %+v", calls)
	}
	if u != (Usage{Input: 100, Output: 20, CacheRead: 64, CacheWrite: 3}) {
		t.Fatalf("usage %+v", u)
	}
}

func TestResponsesStreamEnds(t *testing.T) {
	text := rsEv(`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"output_text","text":"partial"}]}}`)
	cases := []struct {
		name   string
		chunks []string
		check  func(Message, error) bool
	}{
		{"cut off", []string{text, rsEv(`{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`)},
			func(m Message, err error) bool { return err == nil && strings.HasSuffix(m.Text(), CutOffMarker) }},
		{"no completed event", []string{text},
			func(_ Message, err error) bool { return errors.Is(err, ErrIncomplete) }},
		{"failed", []string{rsEv(`{"type":"response.failed","response":{"status":"failed","error":{"code":"server_error","message":"boom"}}}`)},
			func(_ Message, err error) bool {
				return err != nil && strings.Contains(err.Error(), "boom") && Retryable(err)
			}},
		{"error event", []string{rsEv(`{"type":"error","code":"context_length_exceeded","message":"too long"}`)},
			func(_ Message, err error) bool {
				return err != nil && strings.Contains(err.Error(), "too long") && !Retryable(err)
			}},
		{"refusal", []string{rsEv(`{"type":"response.output_item.done","item":{"type":"message","content":[{"type":"refusal","refusal":"no"}]}}`), rsEv(`{"type":"response.completed","response":{"status":"completed"}}`)},
			func(_ Message, err error) bool { return errors.Is(err, errRefused) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			msg, _, _, _, _, err := streamResponses(t, c.chunks, Request{Model: "m"})
			if !c.check(msg, err) {
				t.Fatalf("msg %+v err %v", msg, err)
			}
		})
	}
}

func TestResponsesReplaysOnlyOwnReasoning(t *testing.T) {
	own := json.RawMessage(`{"type":"reasoning","id":"rs_1","encrypted_content":"ENC","summary":[]}`)
	thinking := json.RawMessage(`{"type":"thinking","thinking":"x","signature":"S"}`)
	req := Request{Model: "m", Messages: []Message{
		UserText("hi"),
		{Role: RoleAssistant, Blocks: []Block{
			{Type: BlockReasoning, Raw: own, Model: "m"},
			{Type: BlockReasoning, Raw: own, Model: "other"},
			{Type: BlockReasoning, Raw: thinking, Model: "m"},
			{Type: BlockToolUse, ID: "call_1", Name: "bash"},
		}},
		{Role: RoleUser, Blocks: []Block{{Type: BlockToolResult, ToolUseID: "call_1"}}},
	}}
	got, err := json.Marshal(responsesInput(req))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"content":[{"text":"hi","type":"input_text"}],"role":"user"},` + string(own) +
		`,{"arguments":"{}","call_id":"call_1","name":"bash","type":"function_call"},{"call_id":"call_1","output":"(no output)","type":"function_call_output"}]`
	if string(got) != want {
		t.Fatalf("input\n got %s\nwant %s", got, want)
	}
}
