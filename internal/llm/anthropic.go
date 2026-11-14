package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

type Anthropic struct {
	BaseURL string
	APIKey  string
}

func NewAnthropic(baseURL, apiKey string) *Anthropic {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com"
	}
	baseURL = strings.TrimSuffix(baseURL, "/v1")
	return &Anthropic{BaseURL: baseURL, APIKey: apiKey}
}

var badToolID = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

func toolID(id string) string {
	return badToolID.ReplaceAllString(id, "_")
}

func cacheMark(ttl string) map[string]any {
	m := map[string]any{"type": "ephemeral"}
	if ttl == "1h" {
		m["ttl"] = "1h"
	}
	return m
}

func anthropicImage(b Block) map[string]any {
	return map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": b.MediaType, "data": b.Data}}
}

func (a *Anthropic) content(m Message, model string) []map[string]any {
	var out []map[string]any
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockText:
			if b.Text != "" {
				out = append(out, map[string]any{"type": "text", "text": b.Text})
			}
		case BlockImage:
			out = append(out, anthropicImage(b))
		case BlockReasoning:
			if b.Model != model || len(b.Raw) == 0 {
				continue
			}
			var raw map[string]any
			if json.Unmarshal(b.Raw, &raw) == nil && (raw["type"] == "thinking" || raw["type"] == "redacted_thinking") {
				out = append(out, raw)
			}
		case BlockToolUse:
			input := b.Input
			if len(input) == 0 || InvalidArgs(input) {
				input = json.RawMessage("{}")
			}
			out = append(out, map[string]any{"type": "tool_use", "id": toolID(b.ID), "name": b.Name, "input": input})
		case BlockToolResult:
			text := b.Text
			if text == "" {
				text = "(no output)"
			}
			parts := []any{map[string]any{"type": "text", "text": text}}
			for _, img := range b.Images {
				parts = append(parts, anthropicImage(img))
			}
			r := map[string]any{"type": "tool_result", "tool_use_id": toolID(b.ToolUseID), "content": parts}
			if b.IsError {
				r["is_error"] = true
			}
			out = append(out, r)
		}
	}
	return out
}

func InvalidArgs(raw json.RawMessage) bool {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return true
	}
	_, bad := obj["_invalid_json"]
	return bad
}

var modelVersion = regexp.MustCompile(`(opus|sonnet|haiku|fable|mythos)-(\d)(?:[-.](\d)(?:\D|$))?`)

func supportsAdaptive(model string) bool {
	m := strings.ToLower(model)
	if strings.Contains(m, "claude-3") {
		return false
	}
	v := modelVersion.FindStringSubmatch(m)
	if v == nil {
		return true
	}
	if v[1] == "fable" || v[1] == "mythos" {
		return true
	}
	major := int(v[2][0] - '0')
	minor := 0
	if v[3] != "" {
		minor = int(v[3][0] - '0')
	}
	return major >= 5 || (major == 4 && minor >= 6)
}

func (a *Anthropic) buildBody(req Request) map[string]any {
	var msgs []map[string]any
	for _, m := range MergeConsecutive(req.Messages) {
		c := a.content(m, req.Model)
		if len(c) == 0 {
			continue
		}
		msgs = append(msgs, map[string]any{"role": m.Role, "content": c})
	}
	if n := len(msgs); n > 0 {
		parts := msgs[n-1]["content"].([]map[string]any)
		parts[len(parts)-1]["cache_control"] = cacheMark(req.CacheTTL)
	}
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": req.MaxTokens,
		"stream":     true,
		"messages":   msgs,
	}
	if req.System != "" {
		var system []any
		for _, text := range req.SystemParts() {
			system = append(system, map[string]any{"type": "text", "text": text, "cache_control": cacheMark(req.CacheTTL)})
		}
		body["system"] = system
	}
	if len(req.Tools) > 0 {
		var tools []any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{"name": t.Name, "description": t.Description, "input_schema": t.Schema})
		}
		body["tools"] = tools
	}
	if supportsAdaptive(req.Model) {
		body["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
		if e := anthropicEffort(req.ReasoningEffort); e != "" {
			body["output_config"] = map[string]any{"effort": e}
		}
	}
	return body
}

func anthropicEffort(e string) string {
	switch e {
	case "minimal", "low":
		return "low"
	case "medium", "high", "xhigh", "max":
		return e
	}
	return ""
}

type anthEvent struct {
	Type    string `json:"type"`
	Index   int    `json:"index"`
	Message struct {
		Usage anthUsage `json:"usage"`
	} `json:"message"`
	ContentBlock json.RawMessage `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
	} `json:"delta"`
	Usage *anthUsage `json:"usage"`
	Error *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

type anthUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_input_tokens"`
	CacheReadTokens     int `json:"cache_read_input_tokens"`
}

type anthBlock struct {
	kind  string
	raw   map[string]any
	text  strings.Builder
	sig   strings.Builder
	input strings.Builder
}

func (a *Anthropic) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Message, Usage, error) {
	headers := map[string]string{"x-api-key": a.APIKey, "anthropic-version": "2023-06-01"}
	resp, err := postStream(ctx, a.BaseURL+"/v1/messages", headers, a.buildBody(req))
	if err != nil {
		return Message{}, Usage{}, err
	}
	defer resp.Body.Close()

	var blocks []*anthBlock
	var usage anthUsage
	stop := ""
	done := false
	err = readSSE(resp.Body, func(ev sseEvent) error {
		var e anthEvent
		if err := json.Unmarshal(ev.Data, &e); err != nil {
			return fmt.Errorf("decode stream event: %w", err)
		}
		switch e.Type {
		case "message_start":
			usage = e.Message.Usage
		case "content_block_start":
			var raw map[string]any
			_ = json.Unmarshal(e.ContentBlock, &raw)
			kind, _ := raw["type"].(string)
			for len(blocks) <= e.Index {
				blocks = append(blocks, nil)
			}
			b := &anthBlock{kind: kind, raw: raw}
			if t, ok := raw["text"].(string); ok {
				b.text.WriteString(t)
			}
			blocks[e.Index] = b
		case "content_block_delta":
			if e.Index >= len(blocks) || blocks[e.Index] == nil {
				return nil
			}
			b := blocks[e.Index]
			switch e.Delta.Type {
			case "text_delta":
				b.text.WriteString(e.Delta.Text)
				onDelta(Delta{Kind: DeltaText, Text: e.Delta.Text})
			case "thinking_delta":
				b.text.WriteString(e.Delta.Thinking)
				onDelta(Delta{Kind: DeltaReasoning, Text: e.Delta.Thinking})
			case "signature_delta":
				b.sig.WriteString(e.Delta.Signature)
			case "input_json_delta":
				b.input.WriteString(e.Delta.PartialJSON)
			}
		case "message_delta":
			if e.Delta.StopReason != "" {
				stop = e.Delta.StopReason
			}
			if e.Usage != nil {
				usage.OutputTokens = e.Usage.OutputTokens
				if e.Usage.InputTokens > 0 {
					usage.InputTokens = e.Usage.InputTokens
				}
				if e.Usage.CacheReadTokens > 0 {
					usage.CacheReadTokens = e.Usage.CacheReadTokens
				}
				if e.Usage.CacheCreationTokens > 0 {
					usage.CacheCreationTokens = e.Usage.CacheCreationTokens
				}
			}
		case "message_stop":
			done = true
			return errStreamDone
		case "error":
			if e.Error != nil {
				return fmt.Errorf("stream error: %s: %s", e.Error.Type, e.Error.Message)
			}
			return errors.New("stream error")
		}
		return nil
	})
	u := Usage{
		Input:      usage.InputTokens + usage.CacheReadTokens + usage.CacheCreationTokens,
		Output:     usage.OutputTokens,
		CacheRead:  usage.CacheReadTokens,
		CacheWrite: usage.CacheCreationTokens,
	}
	if err != nil && err != errStreamDone {
		return Message{}, u, streamErr(resp.Body, err)
	}
	if !done {
		return Message{}, u, errors.New("stream ended before the response was complete")
	}
	if stop == "refusal" {
		return Message{}, u, errors.New("the model declined to answer (stop_reason: refusal)")
	}
	msg := Message{Role: "assistant"}
	for i, b := range blocks {
		if b == nil {
			continue
		}
		switch b.kind {
		case "text":
			if b.text.Len() > 0 {
				msg.Blocks = append(msg.Blocks, Block{Type: BlockText, Text: b.text.String()})
			}
		case "thinking":
			raw, _ := json.Marshal(map[string]any{"type": "thinking", "thinking": b.text.String(), "signature": b.sig.String()})
			msg.Blocks = append(msg.Blocks, Block{Type: BlockReasoning, Raw: raw, Model: req.Model})
		case "redacted_thinking":
			raw, _ := json.Marshal(b.raw)
			msg.Blocks = append(msg.Blocks, Block{Type: BlockReasoning, Raw: raw, Model: req.Model})
		case "tool_use":
			id, _ := b.raw["id"].(string)
			if id == "" {
				id = fmt.Sprintf("toolu_%d", i)
			}
			name, _ := b.raw["name"].(string)
			msg.Blocks = append(msg.Blocks, Block{Type: BlockToolUse, ID: id, Name: name, Input: normalizeArgs(b.input.String())})
		}
	}
	if len(msg.ToolUses()) == 0 && msg.Text() == "" {
		if stop == "max_tokens" {
			return Message{}, u, errors.New("the response hit max_tokens before producing any text")
		}
		return Message{}, u, errors.New("the model returned an empty response")
	}
	if stop == "max_tokens" {
		msg.Blocks = append(msg.Blocks, Block{Type: BlockText, Text: "\n[response cut off: max_tokens reached]"})
	}
	return msg, u, nil
}
