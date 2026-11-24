package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/paul-nameless/qi/internal/llm"
)

func imageRound(i int) []llm.Message {
	id := fmt.Sprintf("t%d", i)
	return []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{
			{Type: llm.BlockReasoning, Raw: json.RawMessage(`{"type":"thinking"}`), Model: "m"},
			{Type: llm.BlockToolUse, ID: id, Name: "read", Input: json.RawMessage(`{}`)},
		}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: id, Text: "image", Images: []llm.Block{
			{Type: llm.BlockImage, MediaType: "image/png", Data: "AAAA", Width: 1568, Height: 1008, Source: fmt.Sprintf("shot%d.png", i)},
		}}}},
	}
}

func TestEvictImagesDropsOldestPastTheLimit(t *testing.T) {
	t.Parallel()
	a := newAgent(t, &fakeProvider{})
	for i := 0; i < maxImages; i++ {
		a.conv.messages = append(a.conv.messages, imageRound(i)...)
	}
	a.evictImages()
	if n, _ := imageLoad(a.conv.messages); n != maxImages {
		t.Fatalf("at the limit nothing should change, have %d images", n)
	}
	a.conv.messages = append(a.conv.messages, imageRound(maxImages)...)
	a.evictImages()
	if n, _ := imageLoad(a.conv.messages); n != keepImages {
		t.Fatalf("have %d images after eviction, want %d", n, keepImages)
	}
	first := a.conv.messages[1].Blocks[0]
	if len(first.Images) != 0 || !strings.Contains(first.Text, "[image shot0.png removed") {
		t.Errorf("oldest result: %q", first.Text)
	}
	if len(a.conv.messages[0].Blocks) != 2 {
		t.Error("reasoning before the first change must stay")
	}
	last := a.conv.messages[len(a.conv.messages)-2]
	if last.Blocks[0].Type == llm.BlockReasoning {
		t.Error("reasoning after the first change must be dropped")
	}
	kept := a.conv.messages[len(a.conv.messages)-1].Blocks[0]
	if len(kept.Images) != 1 {
		t.Error("newest image must stay")
	}
}

func TestImageTokensFromSize(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	msgs := []llm.Message{{Role: llm.RoleUser, Blocks: []llm.Block{
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

func TestEvictImagesKeepsTheNewestMessage(t *testing.T) {
	t.Parallel()
	a := newAgent(t, &fakeProvider{})
	big := strings.Repeat("A", 6<<20)
	var imgs []llm.Block
	for i := 0; i < 5; i++ {
		imgs = append(imgs, llm.Block{Type: llm.BlockImage, Data: big, Source: fmt.Sprintf("b%d.webp", i)})
	}
	a.conv.messages = []llm.Message{
		{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockToolUse, ID: "x", Name: "read", Input: json.RawMessage(`{}`)}}},
		{Role: llm.RoleUser, Blocks: []llm.Block{{Type: llm.BlockToolResult, ToolUseID: "x", Text: "images", Images: imgs}}},
	}
	a.evictImages()
	if n, _ := imageLoad(a.conv.messages); n != 5 {
		t.Errorf("images of the newest message were evicted: %d left", n)
	}
}

func TestEvictImagesBySize(t *testing.T) {
	t.Parallel()
	a := newAgent(t, &fakeProvider{})
	for i := 0; i < 30; i++ {
		r := imageRound(i)
		r[1].Blocks[0].Images[0].Data = strings.Repeat("A", 1<<20)
		a.conv.messages = append(a.conv.messages, r...)
	}
	a.evictImages()
	n, size := imageLoad(a.conv.messages)
	if size > keepImageData || n != keepImageData>>20 {
		t.Errorf("after size eviction: %d images, %d bytes", n, size)
	}
}

func TestCompactionEvictsImagesFirst(t *testing.T) {
	t.Parallel()
	p := &fakeProvider{replies: []llm.Message{{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "summary"}}}}}
	a := newAgent(t, p)
	for i := 0; i < 130; i++ {
		a.conv.messages = append(a.conv.messages, imageRound(i)...)
	}
	a.conv.messages = append(a.conv.messages, llm.Message{Role: llm.RoleAssistant, Blocks: []llm.Block{{Type: llm.BlockText, Text: "ok"}}})
	if err := a.Compact(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if n, _ := imageLoad(p.requests[0]); n > maxImages {
		t.Errorf("summary request carried %d images", n)
	}
}
