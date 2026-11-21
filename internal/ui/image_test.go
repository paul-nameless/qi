package ui

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestImagePaths(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	for _, name := range []string{"chart.png", "Shot 2026 at 18.37.png", "b.gif"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	text := "Saved `chart.png` and\n• " + dir + "/Shot 2026 at 18.37.png\nsee ![x](file://" + dir + "/b.gif), https://e.com/chart.png, missing.png, a.pngx"
	want := "chart.png," + dir + "/Shot 2026 at 18.37.png," + dir + "/b.gif"
	if got := strings.Join(imagePaths(text), ","); got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
}

func TestKittyGraphics(t *testing.T) {
	env := func(kv ...string) func(string) string {
		return func(k string) string {
			for i := 0; i < len(kv); i += 2 {
				if kv[i] == k {
					return kv[i+1]
				}
			}
			return ""
		}
	}
	if !KittyGraphics(env("TERM", "xterm-ghostty")) || !KittyGraphics(env("KITTY_WINDOW_ID", "1")) {
		t.Error("ghostty and kitty should support images")
	}
	if KittyGraphics(env("TERM", "xterm-ghostty", "TMUX", "/tmp/t")) || KittyGraphics(env("TERM", "xterm-256color")) {
		t.Error("tmux and plain xterm should not")
	}
}

func TestPrinterDrawsMentionedImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a.png")
	writePNG(t, path, image.NewRGBA(image.Rect(0, 0, 10, 10)))
	out, err := os.Create(filepath.Join(dir, "out"))
	if err != nil {
		t.Fatal(err)
	}
	p := NewPrinter(out)
	p.images = true
	p.Images("Wrote " + path + " and " + path + ", not " + filepath.Join(dir, "none.png") + ".")
	got, _ := os.ReadFile(out.Name())
	if n := strings.Count(string(got), "\x1b_Ga=T"); n != 1 || !strings.HasSuffix(string(got), "\x1b\\\n") {
		t.Errorf("want one image, got %d in %q", n, got)
	}
}

func writePNG(t *testing.T, path string, img image.Image) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestKittyImageRoundTrip(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range src.Pix {
		src.Pix[i] = byte(rng.IntN(256))
	}
	src.SetRGBA(0, 0, color.RGBA{255, 0, 0, 255})
	path := filepath.Join(t.TempDir(), "a.png")
	writePNG(t, path, src)
	seq, err := kittyImage(path, imageBox{cols: 20, rows: 10, cellW: 10, cellH: 20, exact: true})
	if err != nil {
		t.Fatal(err)
	}
	chunks := regexp.MustCompile("\x1b_G([^;]*);([^\x1b]*)\x1b\\\\").FindAllStringSubmatch(seq, -1)
	if len(chunks) < 2 || !strings.HasPrefix(chunks[0][1], "a=T,f=100,q=2,m=1") || chunks[len(chunks)-1][1] != "m=0" {
		t.Fatalf("bad chunking: %d chunks, first %q", len(chunks), chunks[0][1])
	}
	var payload string
	for _, c := range chunks {
		if len(c[2]) > kittyChunk {
			t.Fatalf("chunk of %d bytes", len(c[2]))
		}
		payload += c[2]
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 200 || b.Dy() != 100 {
		t.Errorf("sent %v, want 200×100 to fit 20 cols of 10px", b)
	}
	if _, err := kittyImage(filepath.Join(t.TempDir(), "none.png"), imageBox{cols: 1, rows: 1, cellW: 1, cellH: 1}); err == nil {
		t.Error("a missing file should fail")
	}
}

func TestFitImageWithoutPixels(t *testing.T) {
	w, h, cols, rows := fitImage(100, 100, imageBox{cols: 80, rows: 12, cellW: fallbackCellW, cellH: fallbackCellH})
	if w != 100 || h != 100 || cols != 13 || rows != 7 {
		t.Errorf("got %d×%d in %d×%d cells", w, h, cols, rows)
	}
}
