package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type Responses struct {
	BaseURL string
	APIKey  string
}

func NewResponses(baseURL, apiKey string) *Responses {
	baseURL = strings.TrimRight(baseURL, "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	return &Responses{BaseURL: baseURL, APIKey: apiKey}
}

func (r *Responses) Stream(ctx context.Context, req Request, onDelta func(Delta)) (Message, Usage, error) {
	headers := map[string]string{"Authorization": "Bearer " + r.APIKey}
	resp, err := postStream(ctx, r.BaseURL+"/responses", headers, buildResponsesBody(req))
	if err != nil {
		return Message{}, Usage{}, err
	}
	defer resp.Body.Close()
	return runStream(resp.Body, &responsesStream{model: req.Model, onDelta: onDelta})
}

func buildResponsesBody(req Request) map[string]any {
	reasoning := map[string]any{"summary": "auto"}
	if req.ReasoningEffort != "" {
		reasoning["effort"] = req.ReasoningEffort
	}
	body := map[string]any{
		"model":     req.Model,
		"stream":    true,
		"store":     false,
		"include":   []string{"reasoning.encrypted_content"},
		"reasoning": reasoning,
		"input":     responsesInput(req),
	}
	if req.System != "" {
		body["instructions"] = req.System
	}
	if req.MaxTokens > 0 {
		body["max_output_tokens"] = req.MaxTokens
	}
	if req.SessionID != "" {
		body["prompt_cache_key"] = req.SessionID
	}
	if len(req.Tools) > 0 {
		var tools []any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type":        "function",
				"name":        t.Name,
				"description": t.Description,
				"parameters":  t.Schema,
				"strict":      false,
			})
		}
		body["tools"] = tools
	}
	return body
}

func responsesInput(req Request) []any {
	var out []any
	for _, m := range MergeConsecutive(req.Messages) {
		if m.Role == RoleAssistant {
			out = append(out, assistantItems(m, req.Model)...)
			continue
		}
		var parts []any
		for _, b := range m.Blocks {
			switch b.Type {
			case BlockToolResult:
				out = append(out, toolOutputItem(b))
			case BlockText:
				parts = append(parts, map[string]any{"type": "input_text", "text": b.Text})
			case BlockImage:
				parts = append(parts, inputImage(b))
			}
		}
		if len(parts) > 0 {
			out = append(out, map[string]any{"role": "user", "content": parts})
		}
	}
	return out
}

func inputImage(b Block) map[string]any {
	return map[string]any{"type": "input_image", "image_url": "data:" + b.MediaType + ";base64," + b.Data}
}

func toolOutputItem(b Block) map[string]any {
	text := b.Text
	if text == "" {
		text = "(no output)"
	}
	item := map[string]any{"type": "function_call_output", "call_id": b.ToolUseID, "output": text}
	if len(b.Images) > 0 {
		parts := []any{map[string]any{"type": "input_text", "text": text}}
		for _, img := range b.Images {
			parts = append(parts, inputImage(img))
		}
		item["output"] = parts
	}
	return item
}

func assistantItems(m Message, model string) []any {
	var out []any
	for _, b := range m.Blocks {
		switch b.Type {
		case BlockReasoning:
			if item, ok := replayableReasoning(b, model); ok {
				out = append(out, item)
			}
		case BlockText:
			if b.Text != "" {
				out = append(out, map[string]any{
					"type":    "message",
					"role":    "assistant",
					"content": []any{map[string]any{"type": "output_text", "text": b.Text}},
				})
			}
		case BlockToolUse:
			args := string(b.Input)
			if args == "" {
				args = "{}"
			}
			out = append(out, map[string]any{"type": "function_call", "call_id": b.ID, "name": b.Name, "arguments": args})
		}
	}
	return out
}

func replayableReasoning(b Block, model string) (json.RawMessage, bool) {
	if b.Model != model || len(b.Raw) == 0 {
		return nil, false
	}
	var item struct {
		Type             string `json:"type"`
		EncryptedContent string `json:"encrypted_content"`
	}
	if json.Unmarshal(b.Raw, &item) != nil || item.Type != "reasoning" || item.EncryptedContent == "" {
		return nil, false
	}
	return b.Raw, true
}

type rsItem struct {
	Type      string          `json:"type"`
	CallID    string          `json:"call_id"`
	Name      string          `json:"name"`
	Arguments string          `json:"arguments"`
	Content   []rsContentPart `json:"content"`
}

type rsContentPart struct {
	Type    string `json:"type"`
	Text    string `json:"text"`
	Refusal string `json:"refusal"`
}

type rsError struct {
	Type    string `json:"type"`
	Code    any    `json:"code"`
	Message string `json:"message"`
}

type rsEvent struct {
	Type         string          `json:"type"`
	Delta        string          `json:"delta"`
	SummaryIndex int             `json:"summary_index"`
	Item         json.RawMessage `json:"item"`
	Code         any             `json:"code"`
	Message      string          `json:"message"`
	Response     *struct {
		Status            string   `json:"status"`
		Error             *rsError `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails struct {
				CachedTokens     int `json:"cached_tokens"`
				CacheWriteTokens int `json:"cache_write_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	} `json:"response"`
}

type responsesStream struct {
	model   string
	onDelta func(Delta)
	blocks  []Block
	usage   Usage
	done    bool
	cutOff  bool
	refused bool
}

func (s *responsesStream) handle(ev sseEvent) error {
	var e rsEvent
	if err := json.Unmarshal(ev.Data, &e); err != nil {
		return fmt.Errorf("decode stream event: %w", err)
	}
	switch e.Type {
	case "response.output_text.delta":
		s.onDelta(Delta{Kind: DeltaText, Text: e.Delta})
	case "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
		s.onDelta(Delta{Kind: DeltaReasoning, Text: e.Delta})
	case "response.reasoning_summary_part.added":
		if e.SummaryIndex > 0 {
			s.onDelta(Delta{Kind: DeltaReasoning, Text: "\n\n"})
		}
	case "response.output_item.done":
		return s.item(e.Item)
	case "response.completed", "response.incomplete":
		return s.end(e)
	case "response.failed":
		if e.Response != nil && e.Response.Error != nil {
			return &StreamError{Code: errorCode(e.Response.Error.Code), Message: e.Response.Error.Message}
		}
		return &StreamError{Message: "response failed"}
	case "error":
		return &StreamError{Code: errorCode(e.Code), Message: e.Message}
	}
	return nil
}

func (s *responsesStream) item(raw json.RawMessage) error {
	var it rsItem
	if err := json.Unmarshal(raw, &it); err != nil {
		return fmt.Errorf("decode output item: %w", err)
	}
	switch it.Type {
	case "reasoning":
		s.blocks = append(s.blocks, Block{Type: BlockReasoning, Raw: raw, Model: s.model})
	case "message":
		var text strings.Builder
		for _, p := range it.Content {
			text.WriteString(p.Text)
			if p.Type == "refusal" {
				s.refused = true
			}
		}
		if text.Len() > 0 {
			s.blocks = append(s.blocks, Block{Type: BlockText, Text: text.String()})
		}
	case "function_call":
		s.blocks = append(s.blocks, Block{Type: BlockToolUse, ID: it.CallID, Name: it.Name, Input: normalizeArgs(it.Arguments)})
	}
	return nil
}

func (s *responsesStream) end(e rsEvent) error {
	s.done = true
	if r := e.Response; r != nil {
		if r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "max_output_tokens" {
			s.cutOff = true
		}
		if u := r.Usage; u != nil {
			s.usage = Usage{
				Input:      u.InputTokens,
				Output:     u.OutputTokens,
				CacheRead:  u.InputTokensDetails.CachedTokens,
				CacheWrite: u.InputTokensDetails.CacheWriteTokens,
			}
		}
	}
	return errStreamDone
}

func (s *responsesStream) result() (Message, Usage, streamEnd) {
	return Message{Role: RoleAssistant, Blocks: s.blocks}, s.usage, streamEnd{complete: s.done, cutOff: s.cutOff, refused: s.refused}
}
