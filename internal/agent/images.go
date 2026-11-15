package agent

import (
	"fmt"

	"github.com/paul-nameless/qi/internal/llm"
)

const (
	maxImages       = 100
	maxImageData    = 24 << 20
	keepImages      = 80
	keepImageData   = 14 << 20
	evictedImageFmt = "[image %s removed from the conversation to save space; read it again if you need it]"
	evictedImageAny = "[image removed from the conversation to save space]"
)

func imagePlaceholder(b llm.Block) string {
	if b.Source == "" {
		return evictedImageAny
	}
	return fmt.Sprintf(evictedImageFmt, b.Source)
}

func imageLoad(msgs []llm.Message) (count, size int) {
	for _, m := range msgs {
		for _, b := range m.Blocks {
			if b.Type == llm.BlockImage {
				count++
				size += len(b.Data)
			}
			for _, img := range b.Images {
				count++
				size += len(img.Data)
			}
		}
	}
	return count, size
}

func (a *Agent) evictImages() {
	count, size := imageLoad(a.conv.messages)
	if count <= maxImages && size <= maxImageData {
		return
	}
	removed, first := 0, -1
	for i := 0; i < len(a.conv.messages)-1 && (count > keepImages || size > keepImageData); i++ {
		m, n, s := dropImages(a.conv.messages[i], count-keepImages, size-keepImageData)
		if n == 0 {
			continue
		}
		a.conv.messages[i] = m
		count, size, removed = count-n, size-s, removed+n
		if first < 0 {
			first = i
		}
	}
	if first < 0 {
		return
	}
	for i := first + 1; i < len(a.conv.messages); i++ {
		if a.conv.messages[i].Role == "assistant" {
			a.conv.messages[i] = withoutReasoning(a.conv.messages[i])
		}
	}
	a.SetContextTokens(a.estimateTokens())
	a.ui.Info(fmt.Sprintf("removed the %d oldest images from the context (over %d images or %d MB)", removed, maxImages, maxImageData>>20))
}

func dropImages(m llm.Message, wantCount, wantSize int) (llm.Message, int, int) {
	n, s := 0, 0
	enough := func() bool { return n >= wantCount && s >= wantSize }
	blocks := make([]llm.Block, len(m.Blocks))
	for i, b := range m.Blocks {
		switch {
		case enough():
		case b.Type == llm.BlockImage:
			n, s = n+1, s+len(b.Data)
			b = llm.Block{Type: llm.BlockText, Text: imagePlaceholder(b)}
		case len(b.Images) > 0:
			var kept []llm.Block
			for _, img := range b.Images {
				if enough() {
					kept = append(kept, img)
					continue
				}
				n, s = n+1, s+len(img.Data)
				b.Text += "\n" + imagePlaceholder(img)
			}
			b.Images = kept
		}
		blocks[i] = b
	}
	return llm.Message{Role: m.Role, Blocks: blocks}, n, s
}
