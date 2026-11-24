package llm

import (
	"context"
	"encoding/json"
)

type BlockType string

const (
	BlockText       BlockType = "text"
	BlockReasoning  BlockType = "reasoning_raw"
	BlockToolUse    BlockType = "tool_use"
	BlockToolResult BlockType = "tool_result"
	BlockImage      BlockType = "image"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

type Block struct {
	Type      BlockType       `json:"type"`
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
	Width     int             `json:"width,omitempty"`
	Height    int             `json:"height,omitempty"`
	Source    string          `json:"source,omitempty"`
	Calls     []InnerCall     `json:"calls,omitempty"`
}

type InnerCall struct {
	Tool    string          `json:"tool"`
	Label   string          `json:"label,omitempty"`
	OK      bool            `json:"ok"`
	Summary string          `json:"summary,omitempty"`
	Ms      int64           `json:"ms"`
	Input   json.RawMessage `json:"input,omitempty"`
}

const (
	DefaultImageTokens = 2000
	MaxImageTokens     = 4784
)

func ImageTokens(b Block) int {
	if b.Width <= 0 || b.Height <= 0 {
		return DefaultImageTokens
	}
	return min(((b.Width+27)/28)*((b.Height+27)/28), MaxImageTokens)
}

type Message struct {
	Role   Role    `json:"role"`
	Blocks []Block `json:"blocks"`
}

func UserText(text string) Message {
	return Message{Role: RoleUser, Blocks: []Block{{Type: BlockText, Text: text}}}
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
	SessionSystem   int
	Messages        []Message
	Tools           []ToolDef
	MaxTokens       int
	ReasoningEffort string
	SessionID       string
	CacheTTL        string
}

type Usage struct {
	Input      int      `json:"input"`
	Output     int      `json:"output"`
	CacheRead  int      `json:"cache_read"`
	CacheWrite int      `json:"cache_write"`
	Cost       *float64 `json:"cost,omitempty"`
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

const CutOffMarker = "[response cut off: max_tokens reached]"

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

type SystemPart struct {
	Text  string
	Cache bool
}

func (r Request) SystemParts() []SystemPart {
	cached, tail := r.System, ""
	if r.SessionSystem > 0 && r.SessionSystem < len(r.System) {
		cached, tail = r.System[:r.SessionSystem], r.System[r.SessionSystem:]
	}
	var out []SystemPart
	if r.StaticSystem > 0 && r.StaticSystem < len(cached) {
		out = []SystemPart{{cached[:r.StaticSystem], true}, {cached[r.StaticSystem:], true}}
	} else if cached != "" {
		out = []SystemPart{{cached, true}}
	}
	if tail != "" {
		out = append(out, SystemPart{tail, false})
	}
	return out
}
