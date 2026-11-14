package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sseServer(t *testing.T, chunks []string, check func(*http.Request)) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if check != nil {
			check(r)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, c := range chunks {
			fmt.Fprintf(w, "%s\n\n", c)
		}
	}))
}

func TestStreamAssemblesParallelToolCalls(t *testing.T) {
	chunks := []string{
		": OPENROUTER PROCESSING",
		`data: {"choices":[{"delta":{"content":"Let me look."}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c0","function":{"name":"read","arguments":""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"c1","function":{"name":"bash","arguments":"{\"comm"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"file_path\":"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"arguments":"and\":\"ls\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"a.go\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":100,"completion_tokens":10,"cost":0.5,"prompt_tokens_details":{"cached_tokens":80,"cache_write_tokens":5}}}`,
		`data: [DONE]`,
	}
	srv := sseServer(t, chunks, func(r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth header %q", r.Header.Get("Authorization"))
		}
	})
	defer srv.Close()
	o := NewOpenAI(srv.URL, "k", nil)
	var text strings.Builder
	msg, u, err := o.Stream(context.Background(), Request{Model: "m"}, func(d Delta) {
		if d.Kind == DeltaText {
			text.WriteString(d.Text)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if text.String() != "Let me look." || msg.Text() != "Let me look." {
		t.Fatalf("text %q / %q", text.String(), msg.Text())
	}
	calls := msg.ToolUses()
	if len(calls) != 2 {
		t.Fatalf("calls %+v", calls)
	}
	if calls[0].ID != "c0" || calls[0].Name != "read" || string(calls[0].Input) != `{"file_path":"a.go"}` {
		t.Errorf("call 0 %+v %s", calls[0], calls[0].Input)
	}
	if calls[1].ID != "c1" || calls[1].Name != "bash" || string(calls[1].Input) != `{"command":"ls"}` {
		t.Errorf("call 1 %+v %s", calls[1], calls[1].Input)
	}
	if u != (Usage{Input: 100, Output: 10, CacheRead: 80, CacheWrite: 5, Cost: 0.5}) {
		t.Errorf("usage %+v", u)
	}
}

func TestStreamToolCallsWithoutIndex(t *testing.T) {
	chunks := []string{
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"a","function":{"name":"read","arguments":"{\"file_path\""}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"function":{"name":"read","arguments":":\"x\"}"}}]}}]}`,
		`data: {"choices":[{"delta":{"tool_calls":[{"id":"b","function":{"name":"bash","arguments":"{}"}}]}}]}`,
		`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
	}
	srv := sseServer(t, chunks, nil)
	defer srv.Close()
	msg, _, err := NewOpenAI(srv.URL, "k", nil).Stream(context.Background(), Request{Model: "m"}, func(Delta) {})
	if err != nil {
		t.Fatal(err)
	}
	calls := msg.ToolUses()
	if len(calls) != 2 || calls[0].Name != "read" || string(calls[0].Input) != `{"file_path":"x"}` || calls[1].ID != "b" {
		t.Fatalf("calls %+v", calls)
	}
}

func TestStreamErrors(t *testing.T) {
	cases := map[string][]string{
		"mid-stream error": {`data: {"choices":[{"delta":{"content":"hi"}}]}`, `data: {"error":{"message":"overloaded"}}`},
		"truncated":        {`data: {"choices":[{"delta":{"content":"hi"}}]}`},
		"empty":            {`data: {"choices":[{"delta":{},"finish_reason":"stop"}]}`, `data: [DONE]`},
	}
	for name, chunks := range cases {
		srv := sseServer(t, chunks, nil)
		_, _, err := NewOpenAI(srv.URL, "k", nil).Stream(context.Background(), Request{Model: "m"}, func(Delta) {})
		if err == nil {
			t.Errorf("%s: expected error", name)
		}
		srv.Close()
	}
}

func TestStreamDoesNotRetry400(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		http.Error(w, `{"error":"bad"}`, http.StatusBadRequest)
	}))
	defer srv.Close()
	_, _, err := NewOpenAI(srv.URL, "k", nil).Stream(context.Background(), Request{Model: "m"}, func(Delta) {})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 400 || calls != 1 {
		t.Fatalf("err %v calls %d", err, calls)
	}
}

func bodyJSON(t *testing.T, o *OpenAI, req Request) map[string]any {
	t.Helper()
	data, err := json.Marshal(o.buildBody(req))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	return m
}

func TestBuildBodyCacheAndReasoning(t *testing.T) {
	reasoning := Block{Type: BlockReasoning, Raw: json.RawMessage(`{"type":"reasoning.encrypted","data":"x"}`), Model: "m"}
	other := Block{Type: BlockReasoning, Raw: json.RawMessage(`{"type":"reasoning.encrypted","data":"y"}`), Model: "other"}
	req := Request{
		Model:  "m",
		System: "sys",
		Messages: []Message{
			UserText("hi"),
			{Role: "assistant", Blocks: []Block{reasoning, other, {Type: BlockToolUse, ID: "c1", Name: "read", Input: json.RawMessage(`{}`)}}},
			{Role: "user", Blocks: []Block{{Type: BlockToolResult, ToolUseID: "c1", Text: ""}, {Type: BlockText, Text: "note"}}},
		},
		ReasoningEffort: "low",
		MaxTokens:       10,
	}
	or := NewOpenAI("https://openrouter.ai/api/v1", "k", nil)
	m := bodyJSON(t, or, req)
	msgs := m["messages"].([]any)
	if len(msgs) != 5 {
		t.Fatalf("messages %d: %v", len(msgs), msgs)
	}
	sys := msgs[0].(map[string]any)["content"].([]any)[0].(map[string]any)
	if sys["cache_control"] == nil {
		t.Error("system part has no cache_control")
	}
	asst := msgs[2].(map[string]any)
	if rd := asst["reasoning_details"].([]any); len(rd) != 1 {
		t.Errorf("reasoning_details should only replay same-model blocks: %v", rd)
	}
	tool := msgs[3].(map[string]any)
	if tool["role"] != "tool" || tool["content"].([]any)[0].(map[string]any)["text"] != "(no output)" {
		t.Errorf("tool message %v", tool)
	}
	if tool["content"].([]any)[0].(map[string]any)["cache_control"] != nil {
		t.Error("only the last message should carry cache_control")
	}
	last := msgs[4].(map[string]any)["content"].([]any)[0].(map[string]any)
	if last["cache_control"] == nil || last["text"] != "note" {
		t.Errorf("last part %v", last)
	}
	if m["max_tokens"] == nil || m["reasoning"] == nil || m["reasoning_effort"] != nil {
		t.Errorf("openrouter params %v", m)
	}
	if req.Messages[2].Blocks[1].Text != "note" {
		t.Error("request mutated")
	}

	plain := bodyJSON(t, NewOpenAI("https://api.openai.com/v1", "k", nil), req)
	pm := plain["messages"].([]any)
	if pm[0].(map[string]any)["content"].([]any)[0].(map[string]any)["cache_control"] != nil {
		t.Error("cache_control sent to openai")
	}
	if pm[2].(map[string]any)["reasoning_details"] != nil {
		t.Error("reasoning_details sent to openai")
	}
	if plain["max_completion_tokens"] == nil || plain["reasoning_effort"] != "low" {
		t.Errorf("openai params %v", plain)
	}
}

func TestReasoningMerger(t *testing.T) {
	var r reasoningMerger
	r.add(json.RawMessage(`{"type":"reasoning.summary","index":0,"summary":"Thinking "}`))
	r.add(json.RawMessage(`{"type":"reasoning.summary","index":0,"summary":"hard"}`))
	r.add(json.RawMessage(`{"type":"reasoning.encrypted","index":1,"data":"abc"}`))
	r.add(json.RawMessage(`not json`))
	out := r.result()
	if len(out) != 2 {
		t.Fatalf("got %d items: %s", len(out), out)
	}
	var first map[string]any
	_ = json.Unmarshal(out[0], &first)
	if first["summary"] != "Thinking hard" {
		t.Errorf("merged summary %v", first["summary"])
	}
}

func TestReadSSE(t *testing.T) {
	in := "event: a\r\ndata: one\r\ndata:two\r\n\r\n: keepalive\n\ndata: " + strings.Repeat("x", 100000) + "\n\ndata: last"
	var got []sseEvent
	if err := readSSE(strings.NewReader(in), func(e sseEvent) error { got = append(got, e); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Event != "a" || string(got[0].Data) != "one\ntwo" || len(got[1].Data) != 100000 || string(got[2].Data) != "last" {
		t.Fatalf("events %d %+v", len(got), got[0])
	}
}

func TestRetryable(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{&APIError{Status: 400}, false},
		{&APIError{Status: 429}, true},
		{&APIError{Status: 503}, true},
		{context.Canceled, false},
		{fmt.Errorf("dial: %w", errors.New("refused")), true},
	}
	for _, c := range cases {
		if got := retryable(c.err); got != c.want {
			t.Errorf("%v: got %v", c.err, got)
		}
	}
}
