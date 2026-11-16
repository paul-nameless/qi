package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type OpenAI struct {
	BaseURL      string
	APIKey       string
	CacheControl bool
	OpenRouter   bool
}

func NewOpenAI(baseURL, apiKey string, cacheControl *bool) *OpenAI {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	isOR := strings.Contains(baseURL, "openrouter.ai")
	cc := isOR
	if cacheControl != nil {
		cc = *cacheControl
	}
	return &OpenAI{BaseURL: baseURL, APIKey: apiKey, CacheControl: cc, OpenRouter: isOR}
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (o *OpenAI) buildMessages(req Request) []map[string]any {
	var out []map[string]any
	if req.System != "" {
		var parts []any
		for _, sp := range req.SystemParts() {
			part := map[string]any{"type": "text", "text": sp.Text}
			if o.CacheControl && sp.Cache {
				part["cache_control"] = cacheMark(req.CacheTTL)
			}
			parts = append(parts, part)
		}
		out = append(out, map[string]any{"role": "system", "content": parts})
	}
	for _, m := range MergeConsecutive(req.Messages) {
		if m.Role == RoleAssistant {
			out = append(out, o.assistantMessage(m, req.Model))
			continue
		}
		var parts []any
		var toolImages []any
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockToolResult:
				text := b.Text
				if text == "" {
					text = "(no output)"
				}
				out = append(out, map[string]any{
					"role":         "tool",
					"tool_call_id": b.ToolUseID,
					"content":      []any{map[string]any{"type": "text", "text": text}},
				})
				for _, img := range b.Images {
					toolImages = append(toolImages, imagePart(img))
				}
			case BlockText:
				parts = append(parts, map[string]any{"type": "text", "text": b.Text})
			case BlockImage:
				parts = append(parts, imagePart(b))
			}
		}
		if len(toolImages) > 0 {
			parts = append([]any{map[string]any{"type": "text", "text": "Images returned by the tool calls above:"}}, append(toolImages, parts...)...)
		}
		if len(parts) > 0 {
			out = append(out, map[string]any{"role": "user", "content": parts})
		}
	}
	if o.CacheControl && len(out) > 0 {
		last := out[len(out)-1]
		if parts, ok := last["content"].([]any); ok && len(parts) > 0 {
			if p, ok := parts[len(parts)-1].(map[string]any); ok {
				p["cache_control"] = cacheMark(req.CacheTTL)
			}
		}
	}
	return out
}

func imagePart(b Block) map[string]any {
	return map[string]any{
		"type":      "image_url",
		"image_url": map[string]any{"url": "data:" + b.MediaType + ";base64," + b.Data},
	}
}

func (o *OpenAI) assistantMessage(m Message, model string) map[string]any {
	msg := map[string]any{"role": "assistant"}
	var text strings.Builder
	var calls []oaToolCall
	var details []json.RawMessage
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockText:
			text.WriteString(b.Text)
		case BlockToolUse:
			var c oaToolCall
			c.ID = b.ID
			c.Type = "function"
			c.Function.Name = b.Name
			c.Function.Arguments = string(b.Input)
			if len(b.Input) == 0 {
				c.Function.Arguments = "{}"
			}
			calls = append(calls, c)
		case BlockReasoning:
			if b.Model == model && len(b.Raw) > 0 {
				details = append(details, b.Raw)
			}
		}
	}
	msg["content"] = text.String()
	if len(calls) > 0 {
		msg["tool_calls"] = calls
	}
	if len(details) > 0 && o.OpenRouter {
		msg["reasoning_details"] = details
	}
	return msg
}

func (o *OpenAI) buildBody(req Request) map[string]any {
	body := map[string]any{
		"model":          req.Model,
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
		"messages":       o.buildMessages(req),
	}
	if len(req.Tools) > 0 {
		var tools []any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Schema,
				},
			})
		}
		body["tools"] = tools
	}
	if o.OpenRouter {
		body["usage"] = map[string]any{"include": true}
		if req.ReasoningEffort != "" {
			body["reasoning"] = map[string]any{"effort": req.ReasoningEffort}
		}
		if req.MaxTokens > 0 {
			body["max_tokens"] = req.MaxTokens
		}
	} else {
		if req.ReasoningEffort != "" {
			body["reasoning_effort"] = req.ReasoningEffort
		}
		if req.MaxTokens > 0 {
			body["max_completion_tokens"] = req.MaxTokens
		}
	}
	return body
}

type oaToolDelta struct {
	Index    *int   `json:"index"`
	ID       string `json:"id"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaDelta struct {
	Content          *string           `json:"content"`
	Reasoning        *string           `json:"reasoning"`
	ReasoningContent *string           `json:"reasoning_content"`
	ReasoningDetails []json.RawMessage `json:"reasoning_details"`
	ToolCalls        []oaToolDelta     `json:"tool_calls"`
}

type oaChunk struct {
	Choices []struct {
		Delta        oaDelta `json:"delta"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens        int     `json:"prompt_tokens"`
		CompletionTokens    int     `json:"completion_tokens"`
		Cost                float64 `json:"cost"`
		PromptTokensDetails struct {
			CachedTokens     int `json:"cached_tokens"`
			CacheWriteTokens int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func errorCode(code any) string {
	if code == nil {
		return ""
	}
	return fmt.Sprint(code)
}

type callSlot struct {
	index int
	id    string
	name  string
	args  strings.Builder
}

func (o *OpenAI) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Message, Usage, error) {
	headers := map[string]string{"Authorization": "Bearer " + o.APIKey}
	if o.OpenRouter {
		headers["X-Title"] = "qi"
		if req.SessionID != "" {
			headers["x-session-id"] = req.SessionID
		}
	}
	resp, err := postStream(ctx, o.BaseURL+"/chat/completions", headers, o.buildBody(req))
	if err != nil {
		return Message{}, Usage{}, err
	}
	defer resp.Body.Close()
	return runStream(resp.Body, &openAIStream{model: req.Model, onDelta: onDelta})
}

type openAIStream struct {
	model   string
	onDelta func(Delta)
	text    strings.Builder
	slots   []*callSlot
	details reasoningMerger
	usage   Usage
	finish  string
	done    bool
}

func (s *openAIStream) handle(ev sseEvent) error {
	if string(ev.Data) == "[DONE]" {
		s.done = true
		return errStreamDone
	}
	var ch oaChunk
	if err := json.Unmarshal(ev.Data, &ch); err != nil {
		return fmt.Errorf("decode stream chunk: %w", err)
	}
	if ch.Error != nil {
		return &StreamError{Code: errorCode(ch.Error.Code), Message: ch.Error.Message}
	}
	if ch.Usage != nil {
		s.usage = Usage{
			Input:      ch.Usage.PromptTokens,
			Output:     ch.Usage.CompletionTokens,
			CacheRead:  ch.Usage.PromptTokensDetails.CachedTokens,
			CacheWrite: ch.Usage.PromptTokensDetails.CacheWriteTokens,
			Cost:       ch.Usage.Cost,
		}
	}
	for _, c := range ch.Choices {
		if c.FinishReason != nil && *c.FinishReason != "" {
			s.finish = *c.FinishReason
		}
		s.delta(c.Delta)
	}
	return nil
}

func (s *openAIStream) delta(d oaDelta) {
	reasoningShown := false
	for _, r := range []*string{d.Reasoning, d.ReasoningContent} {
		if r != nil && *r != "" {
			s.onDelta(Delta{Kind: DeltaReasoning, Text: *r})
			reasoningShown = true
			break
		}
	}
	for _, raw := range d.ReasoningDetails {
		if t := s.details.add(raw); t != "" && !reasoningShown {
			s.onDelta(Delta{Kind: DeltaReasoning, Text: t})
		}
	}
	if d.Content != nil && *d.Content != "" {
		s.text.WriteString(*d.Content)
		s.onDelta(Delta{Kind: DeltaText, Text: *d.Content})
	}
	for _, tc := range d.ToolCalls {
		s.toolCall(tc)
	}
}

func (s *openAIStream) toolCall(tc oaToolDelta) {
	slot := findSlot(s.slots, tc.Index, tc.ID)
	if slot == nil {
		idx := len(s.slots)
		if tc.Index != nil {
			idx = *tc.Index
		}
		slot = &callSlot{index: idx, id: tc.ID}
		s.slots = append(s.slots, slot)
	}
	if slot.name == "" {
		slot.name = tc.Function.Name
	}
	slot.args.WriteString(tc.Function.Arguments)
}

func (s *openAIStream) result() (Message, Usage, streamEnd) {
	msg := Message{Role: RoleAssistant}
	for _, raw := range s.details.result() {
		msg.Blocks = append(msg.Blocks, Block{Type: BlockReasoning, Raw: raw, Model: s.model})
	}
	if s.text.Len() > 0 {
		msg.Blocks = append(msg.Blocks, Block{Type: BlockText, Text: s.text.String()})
	}
	for i, slot := range s.slots {
		id := slot.id
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		msg.Blocks = append(msg.Blocks, Block{Type: BlockToolUse, ID: id, Name: slot.name, Input: normalizeArgs(slot.args.String())})
	}
	return msg, s.usage, streamEnd{complete: s.done || s.finish != "", cutOff: s.finish == "length"}
}

func findSlot(slots []*callSlot, index *int, id string) *callSlot {
	if index == nil {
		if id != "" {
			for _, s := range slots {
				if s.id == id {
					return s
				}
			}
			return nil
		}
		if len(slots) > 0 {
			return slots[len(slots)-1]
		}
		return nil
	}
	for i := len(slots) - 1; i >= 0; i-- {
		s := slots[i]
		if s.index != *index {
			continue
		}
		if id != "" && s.id != "" && s.id != id {
			return nil
		}
		if id != "" && s.id == "" {
			s.id = id
		}
		return s
	}
	return nil
}

func normalizeArgs(args string) json.RawMessage {
	args = strings.TrimSpace(args)
	if args == "" {
		return json.RawMessage("{}")
	}
	if json.Valid([]byte(args)) {
		return json.RawMessage(args)
	}
	wrapped, _ := json.Marshal(map[string]string{"_invalid_json": args})
	return wrapped
}

type reasoningMerger struct {
	items []map[string]any
	byIdx map[float64]int
}

func (r *reasoningMerger) add(raw json.RawMessage) string {
	var item map[string]any
	if err := json.Unmarshal(raw, &item); err != nil {
		return ""
	}
	if r.byIdx == nil {
		r.byIdx = map[float64]int{}
	}
	shown := ""
	for _, k := range []string{"summary", "text"} {
		if s, ok := item[k].(string); ok {
			shown += s
		}
	}
	idx, hasIdx := item["index"].(float64)
	if pos, ok := r.byIdx[idx]; hasIdx && ok {
		cur := r.items[pos]
		for k, v := range item {
			if s, ok := v.(string); ok && (k == "summary" || k == "text" || k == "data") {
				prev, _ := cur[k].(string)
				cur[k] = prev + s
				continue
			}
			cur[k] = v
		}
		return shown
	}
	r.items = append(r.items, item)
	if hasIdx {
		r.byIdx[idx] = len(r.items) - 1
	}
	return shown
}

func (r *reasoningMerger) result() []json.RawMessage {
	var out []json.RawMessage
	for _, it := range r.items {
		b, err := json.Marshal(it)
		if err == nil {
			out = append(out, b)
		}
	}
	return out
}
