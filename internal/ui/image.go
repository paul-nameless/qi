package ui

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/paul-nameless/qi/internal/imgutil"
)

const (
	maxImageFile      = 50 << 20
	maxImagePixels    = 40_000_000
	kittyChunk        = 4096
	fallbackCellW     = 8
	fallbackCellH     = 16
	maxImagesPerBlock = 4
)

type imageBox struct {
	cols, rows   int
	cellW, cellH int
	exact        bool
}

func KittyGraphics(getenv func(string) string) bool {
	if getenv("TMUX") != "" || getenv("STY") != "" {
		return false
	}
	switch getenv("TERM") {
	case "xterm-kitty", "xterm-ghostty":
		return true
	}
	return getenv("KITTY_WINDOW_ID") != ""
}

var imageExtRE = regexp.MustCompile(`(?i)\.(png|jpe?g|gif)\b`)

func imagePaths(text string) []string {
	var out []string
	for _, m := range imageExtRE.FindAllStringIndex(text, -1) {
		start := strings.LastIndexAny(text[:m[1]], "`'\"()<>[]{},;|*\n") + 1
		if p, ok := existingImage(text[start:m[1]]); ok {
			out = append(out, p)
		}
	}
	return out
}

func existingImage(s string) (string, bool) {
	for {
		p := strings.TrimPrefix(s, "file://")
		if path, err := imagePath(p); err == nil && !strings.Contains(p, "://") {
			if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
				return p, true
			}
		}
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			return "", false
		}
		s = s[i+1:]
	}
}

func imagePath(src string) (string, error) {
	if rest, ok := strings.CutPrefix(src, "~/"); ok {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, rest), nil
	}
	return filepath.Abs(src)
}

func kittyImage(src string, box imageBox) (string, error) {
	path, err := imagePath(src)
	if err != nil {
		return "", err
	}
	img, err := loadImage(path)
	if err != nil {
		return "", err
	}
	w, h, cols, rows := fitImage(img.Bounds().Dx(), img.Bounds().Dy(), box)
	small := imgutil.Resize(img, image.Rect(0, 0, img.Bounds().Dx(), img.Bounds().Dy()), w, h)
	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		return "", fmt.Errorf("encode png: %w", err)
	}
	head := "a=T,f=100,q=2"
	if !box.exact {
		head += fmt.Sprintf(",c=%d,r=%d", cols, rows)
	}
	return kittySeq(head, base64.StdEncoding.EncodeToString(buf.Bytes())), nil
}

func loadImage(path string) (image.Image, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxImageFile {
		return nil, errors.New("not a regular file under 50 MB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxImagePixels || cfg.Width == 0 || cfg.Height == 0 {
		return nil, fmt.Errorf("image is %d×%d", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	return img, err
}

func fitImage(w, h int, box imageBox) (int, int, int, int) {
	maxW, maxH := float64(box.cols*box.cellW), float64(box.rows*box.cellH)
	s := math.Min(1, math.Min(maxW/float64(w), maxH/float64(h)))
	dw := max(1, int(math.Round(float64(w)*s)))
	dh := max(1, int(math.Round(float64(h)*s)))
	cols := min(box.cols, max(1, (dw+box.cellW-1)/box.cellW))
	rows := min(box.rows, max(1, (dh+box.cellH-1)/box.cellH))
	return dw, dh, cols, rows
}

func kittySeq(head, payload string) string {
	var b strings.Builder
	for first := true; first || payload != ""; first = false {
		n := min(kittyChunk, len(payload))
		more := 0
		if n < len(payload) {
			more = 1
		}
		ctrl := fmt.Sprintf("m=%d", more)
		if first {
			ctrl = head + "," + ctrl
		}
		b.WriteString("\x1b_G" + ctrl + ";" + payload[:n] + "\x1b\\")
		payload = payload[n:]
	}
	return b.String()
}
