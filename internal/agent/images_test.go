package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/paul-nameless/aicode/internal/llm"
)

func imageRound(i int) []llm.Message {
	id := fmt.Sprintf("t%d", i)
	return []llm.Message{
		{Role: "assistant", Blocks: []llm.Block{
			{Type: llm.BlockReasoning, Raw: json.RawMessage(`{"type":"thinking"}`), Model: "m"},
			{Type: llm.BlockToolUse, ID: id, Name: "read", Input: json.RawMessage(`{}`)},
		}},
		{Role: "user", Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: id, Text: "image", Images: []llm.Block{
			{Type: llm.BlockImage, MediaType: "image/png", Data: "AAAA", Width: 1568, Height: 1008, Source: fmt.Sprintf("shot%d.png", i)},
		}}}},
	}
}

func TestEvictImagesDropsOldestPastTheLimit(t *testing.T) {
	a := newAgent(t, &fakeProvider{})
	for i := 0; i < maxImages; i++ {
		a.Messages = append(a.Messages, imageRound(i)...)
	}
	a.evictImages()
	if n, _ := imageLoad(a.Messages); n != maxImages {
		t.Fatalf("at the limit nothing should change, have %d images", n)
	}
	a.Messages = append(a.Messages, imageRound(maxImages)...)
	a.evictImages()
	if n, _ := imageLoad(a.Messages); n != keepImages {
		t.Fatalf("have %d images after eviction, want %d", n, keepImages)
	}
	first := a.Messages[1].Blocks[0]
	if len(first.Images) != 0 || !strings.Contains(first.Text, "[image shot0.png removed") {
		t.Errorf("oldest result: %q", first.Text)
	}
	if len(a.Messages[0].Blocks) != 2 {
		t.Error("reasoning before the first change must stay")
	}
	last := a.Messages[len(a.Messages)-2]
	if last.Blocks[0].Type == llm.BlockReasoning {
		t.Error("reasoning after the first change must be dropped")
	}
	kept := a.Messages[len(a.Messages)-1].Blocks[0]
	if len(kept.Images) != 1 {
		t.Error("newest image must stay")
	}
}

func TestImageTokensFromSize(t *testing.T) {
	cases := []struct {
		w, h, want int
	}{
		{1568, 1008, 56 * 36},
		{2800, 1800, llm.MaxImageTokens},
		{0, 0, llm.DefaultImageTokens},
	}
	for _, c := range cases {
		if got := llm.ImageTokens(llm.Block{Width: c.w, Height: c.h}); got != c.want {
			t.Errorf("%dx%d: %d, want %d", c.w, c.h, got, c.want)
		}
	}
	blocks := []llm.Block{{Type: llm.BlockImage, Width: 1400, Height: 900}}
	if got := blockChars(blocks) / 4; got != 50*33 {
		t.Errorf("blockChars counts %d tokens", got)
	}
}

func TestOverflowClipRemovesPastedImages(t *testing.T) {
	msgs := []llm.Message{{Role: "user", Blocks: []llm.Block{
		{Type: llm.BlockText, Text: "look"},
		{Type: llm.BlockImage, Data: "AAAA", Source: "/tmp/a.png"},
	}}}
	out := clipHistory(msgs)
	if n, _ := imageLoad(out); n != 0 {
		t.Fatal("clipped history still has images")
	}
	if !strings.Contains(out[0].Blocks[1].Text, "/tmp/a.png") {
		t.Errorf("placeholder: %q", out[0].Blocks[1].Text)
	}
	if n, _ := imageLoad(msgs); n != 1 {
		t.Error("clipping must not change the history")
	}
}
