package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAnthropicStream(t *testing.T) {
	chunks := []string{
		"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"cache_read_input_tokens\":80,\"cache_creation_input_tokens\":5}}}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"hmm\"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"SIG\"}}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"Reading.\"}}",
		"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":2,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"read\",\"input\":{}}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"file_path\\\":\"}}",
		"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":2,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"a.go\\\"}\"}}",
		"event: ping\ndata: {\"type\":\"ping\"}",
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":20}}",
		"event: message_stop\ndata: {\"type\":\"message_stop\"}",
	}
	var body map[string]any
	srv := sseServer(t, chunks, func(r *http.Request) {
		if r.URL.Path != "/v1/messages" || r.Header.Get("x-api-key") != "k" || r.Header.Get("anthropic-version") == "" {
			t.Errorf("request %s %v", r.URL.Path, r.Header)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
	})
	defer srv.Close()
	a := NewAnthropic(srv.URL, "k")
	req := Request{
		Model: "claude-haiku-5-5", System: "sys", MaxTokens: 100, ReasoningEffort: "low", CacheTTL: "1h",
		Messages: []Message{
			UserText("hi"),
			{Role: "assistant", Blocks: []Block{{Type: BlockToolUse, ID: "call:abc.1", Name: "bash", Input: json.RawMessage(`{"command":"ls"}`)}}},
			{Role: "user", Blocks: []Block{{Type: BlockToolResult, ToolUseID: "call:abc.1", Text: "out", Images: []Block{{Type: BlockImage, MediaType: "image/png", Data: "AAAA"}}}}},
		},
	}
	msg, u, err := a.Stream(context.Background(), req, func(Delta) {})
	if err != nil {
		t.Fatal(err)
	}
	if u != (Usage{Input: 95, Output: 20, CacheRead: 80, CacheWrite: 5}) {
		t.Errorf("usage %+v", u)
	}
	if len(msg.Blocks) != 3 || msg.Blocks[0].Type != BlockReasoning || !strings.Contains(string(msg.Blocks[0].Raw), `"signature":"SIG"`) {
		t.Fatalf("blocks %+v", msg.Blocks)
	}
	if c := msg.ToolUses(); len(c) != 1 || string(c[0].Input) != `{"file_path":"a.go"}` {
		t.Errorf("tool use %+v", c)
	}
	msgs := body["messages"].([]any)
	asst := msgs[1].(map[string]any)["content"].([]any)[0].(map[string]any)
	if asst["id"] != "call_abc_1" {
		t.Errorf("tool id not sanitized: %v", asst["id"])
	}
	res := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if res["tool_use_id"] != "call_abc_1" || len(res["content"].([]any)) != 2 || res["cache_control"] == nil {
		t.Errorf("tool result %v", res)
	}
	if body["thinking"].(map[string]any)["type"] != "adaptive" || body["output_config"].(map[string]any)["effort"] != "low" {
		t.Errorf("thinking params %v %v", body["thinking"], body["output_config"])
	}
	sys := body["system"].([]any)[0].(map[string]any)["cache_control"].(map[string]any)
	if sys["ttl"] != "1h" {
		t.Errorf("system cache %v", sys)
	}
}

func TestAnthropicReplaysThinkingOnlyForSameModel(t *testing.T) {
	a := NewAnthropic("", "k")
	think := Block{Type: BlockReasoning, Raw: json.RawMessage(`{"type":"thinking","thinking":"x","signature":"s"}`), Model: "claude-opus-5-5"}
	foreign := Block{Type: BlockReasoning, Raw: json.RawMessage(`{"type":"reasoning.encrypted","data":"y"}`), Model: "claude-opus-5-5"}
	m := Message{Role: "assistant", Blocks: []Block{think, foreign, {Type: BlockText, Text: "a"}}}
	if c := a.content(m, "claude-opus-5-5"); len(c) != 2 || c[0]["type"] != "thinking" {
		t.Errorf("same model: %v", c)
	}
	if c := a.content(m, "claude-sonnet-5-5"); len(c) != 1 {
		t.Errorf("other model: %v", c)
	}
}

func TestAnthropicStreamErrors(t *testing.T) {
	start := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":1}}}"
	text := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}"
	stop := func(reason string) string {
		return "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"" + reason + "\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}"
	}
	cases := map[string][]string{
		"truncated":       {start, text},
		"error event":     {start, "event: error\ndata: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"busy\"}}"},
		"refusal":         {start, text, stop("refusal")},
		"empty":           {start, stop("end_turn")},
		"max_tokens only": {start, stop("max_tokens")},
	}
	for name, chunks := range cases {
		srv := sseServer(t, chunks, nil)
		_, _, err := NewAnthropic(srv.URL, "k").Stream(context.Background(), Request{Model: "claude-haiku-5-5", MaxTokens: 10}, func(Delta) {})
		if err == nil {
			t.Errorf("%s: expected error", name)
		}
		srv.Close()
	}
	srv := sseServer(t, []string{start, text, stop("max_tokens")}, nil)
	defer srv.Close()
	msg, _, err := NewAnthropic(srv.URL, "k").Stream(context.Background(), Request{Model: "claude-haiku-5-5", MaxTokens: 10}, func(Delta) {})
	if err != nil || !strings.Contains(msg.Text(), "partial") || !strings.Contains(msg.Text(), "cut off") {
		t.Errorf("max_tokens with text: %q %v", msg.Text(), err)
	}
}

func TestSupportsAdaptive(t *testing.T) {
	cases := map[string]bool{
		"claude-opus-4-20250514":      false,
		"claude-opus-4-1-20250805":    false,
		"claude-sonnet-4-20250514":    false,
		"claude-sonnet-4-5":           false,
		"claude-haiku-4-5":            false,
		"claude-3-7-sonnet-latest":    false,
		"claude-opus-4-6":             true,
		"claude-sonnet-4-6":           true,
		"claude-opus-4-8":             true,
		"claude-haiku-5-5":            true,
		"anthropic/claude-haiku-5.5":  true,
		"claude-fable-5-2":            true,
		"claude-sonnet-5":             true,
		"anthropic/claude-sonnet-4.5": false,
	}
	for m, want := range cases {
		if got := supportsAdaptive(m); got != want {
			t.Errorf("supportsAdaptive(%q) = %v, want %v", m, got, want)
		}
	}
}
