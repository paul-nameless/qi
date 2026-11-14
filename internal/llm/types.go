package llm

import (
	"context"
	"encoding/json"
)

const (
	BlockText       = "text"
	BlockReasoning  = "reasoning_raw"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"
	BlockImage      = "image"
)

type Block struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Raw       json.RawMessage `json:"raw,omitempty"`
	Model     string          `json:"model,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
	Images    []Block         `json:"images,omitempty"`
	MediaType string          `json:"media_type,omitempty"`
	Data      string          `json:"data,omitempty"`
}

type Message struct {
	Role   string  `json:"role"`
	Blocks []Block `json:"blocks"`
}

func UserText(text string) Message {
	return Message{Role: "user", Blocks: []Block{{Type: BlockText, Text: text}}}
}

func (m Message) Text() string {
	var s string
	for _, b := range m.Blocks {
		if b.Type == BlockText {
			s += b.Text
		}
	}
	return s
}

func (m Message) ToolUses() []Block {
	var out []Block
	for _, b := range m.Blocks {
		if b.Type == BlockToolUse {
			out = append(out, b)
		}
	}
	return out
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type Request struct {
	Model           string
	System          string
	StaticSystem    int
	Messages        []Message
	Tools           []ToolDef
	MaxTokens       int
	ReasoningEffort string
	SessionID       string
	CacheTTL        string
}

type Usage struct {
	Input      int     `json:"input"`
	Output     int     `json:"output"`
	CacheRead  int     `json:"cache_read"`
	CacheWrite int     `json:"cache_write"`
	Cost       float64 `json:"cost,omitempty"`
}

func (u *Usage) Add(o Usage) {
	u.Input += o.Input
	u.Output += o.Output
	u.CacheRead += o.CacheRead
	u.CacheWrite += o.CacheWrite
	u.Cost += o.Cost
}

const (
	DeltaText      = "text"
	DeltaReasoning = "reasoning"
)

type Delta struct {
	Kind string
	Text string
}

type Provider interface {
	Stream(ctx context.Context, req Request, onDelta func(Delta)) (Message, Usage, error)
}

func MergeConsecutive(msgs []Message) []Message {
	var out []Message
	for _, m := range msgs {
		if n := len(out); n > 0 && out[n-1].Role == m.Role {
			merged := out[n-1]
			merged.Blocks = append(append([]Block{}, merged.Blocks...), m.Blocks...)
			out[n-1] = merged
			continue
		}
		out = append(out, m)
	}
	return out
}

func (r Request) SystemParts() []string {
	if r.StaticSystem <= 0 || r.StaticSystem >= len(r.System) {
		return []string{r.System}
	}
	return []string{r.System[:r.StaticSystem], r.System[r.StaticSystem:]}
}
