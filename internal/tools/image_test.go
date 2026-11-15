package tools

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testPNG(t *testing.T, w, h int, fill func(x, y int) color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, fill(x, y))
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeBlock(t *testing.T, data string) image.Image {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		t.Fatal(err)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func solid(c color.RGBA) func(int, int) color.RGBA {
	return func(int, int) color.RGBA { return c }
}

func TestPrepareImageScalesLargeImages(t *testing.T) {
	data := testPNG(t, 2800, 1800, solid(color.RGBA{10, 120, 200, 255}))
	img, err := prepareImage(data, "image/png", "shot.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	b := img.block
	if b.Width != 1568 || b.Height != 1008 || b.MediaType != "image/png" || b.Source != "shot.png" {
		t.Fatalf("got %dx%d %s %q", b.Width, b.Height, b.MediaType, b.Source)
	}
	got := decodeBlock(t, b.Data)
	if got.Bounds().Dx() != 1568 || got.Bounds().Dy() != 1008 {
		t.Errorf("decoded size %v", got.Bounds())
	}
	if r, g, bl, _ := got.At(700, 500).RGBA(); r>>8 != 10 || g>>8 != 120 || bl>>8 != 200 {
		t.Errorf("colour changed to %d,%d,%d", r>>8, g>>8, bl>>8)
	}
	if !strings.Contains(img.note, "2800×1800, sent as 1568×1008; a point (x, y) in it is (x×1.7857, y×1.7857) in the original; pass crop") {
		t.Errorf("note: %s", img.note)
	}
}

func TestPrepareImageKeepsSmallImagesAsIs(t *testing.T) {
	data := testPNG(t, 300, 200, solid(color.RGBA{1, 2, 3, 255}))
	img, err := prepareImage(data, "image/png", "small.png", nil)
	if err != nil {
		t.Fatal(err)
	}
	if img.block.Data != base64.StdEncoding.EncodeToString(data) || img.block.Width != 300 || img.block.Height != 200 {
		t.Errorf("small image was changed")
	}
	if img.note != "image small.png 300×200" {
		t.Errorf("note: %s", img.note)
	}
}

func TestPrepareImageCropsAtFullResolution(t *testing.T) {
	data := testPNG(t, 2800, 1800, func(x, y int) color.RGBA {
		if x >= 1000 {
			return color.RGBA{255, 0, 0, 255}
		}
		return color.RGBA{0, 0, 255, 255}
	})
	img, err := prepareImage(data, "image/png", "shot.png", []int{990, 100, 20, 10})
	if err != nil {
		t.Fatal(err)
	}
	got := decodeBlock(t, img.block.Data)
	if got.Bounds().Dx() != 20 || got.Bounds().Dy() != 10 {
		t.Fatalf("crop size %v", got.Bounds())
	}
	if r, _, b, _ := got.At(9, 0).RGBA(); b>>8 != 255 || r != 0 {
		t.Errorf("left of the edge should be blue")
	}
	if r, _, _, _ := got.At(10, 0).RGBA(); r>>8 != 255 {
		t.Errorf("right of the edge should be red")
	}
	if !strings.Contains(img.note, "region x=990 y=100 20×10 of image shot.png (2800×1800), full resolution") {
		t.Errorf("note: %s", img.note)
	}
	if _, err := prepareImage(data, "image/png", "shot.png", []int{5000, 0, 10, 10}); err == nil {
		t.Error("crop outside the image should fail")
	}
	for _, bad := range [][]int{{0, 0, 10}, {-1, 0, 10, 10}, {math.MaxInt, 0, 1, 1}, {0, 0, math.MaxInt, 0}} {
		if _, err := prepareImage(data, "image/png", "shot.png", bad); err == nil {
			t.Errorf("crop %v should fail", bad)
		}
	}
	wide, err := prepareImage(data, "image/png", "shot.png", []int{1000, 100, 1800, 1000})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(wide.note, "region x=1000 y=100 1800×1000") || !strings.Contains(wide.note, "(1000 + x×1.1480, 100 + y×1.1480)") || wide.block.MediaType != "image/png" {
		t.Errorf("scaled crop note: %s (%s)", wide.note, wide.block.MediaType)
	}
	edge, err := prepareImage(data, "image/png", "shot.png", []int{2700, 1700, math.MaxInt, math.MaxInt})
	if err != nil || edge.block.Width != 100 || edge.block.Height != 100 {
		t.Errorf("crop past the edge should clamp: %v %dx%d", err, edge.block.Width, edge.block.Height)
	}
}

func TestPrepareImageKeepsJPEGAsJPEG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 2000, 1000))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	img, err := prepareImage(buf.Bytes(), "image/jpeg", "photo.jpg", nil)
	if err != nil {
		t.Fatal(err)
	}
	if img.block.MediaType != "image/jpeg" || img.block.Width != 1568 || img.block.Height != 784 {
		t.Errorf("got %s %dx%d", img.block.MediaType, img.block.Width, img.block.Height)
	}
}

func TestPrepareImagePassesUndecodableThrough(t *testing.T) {
	data := []byte("RIFF\x00\x00\x00\x00WEBPVP8 not really")
	img, err := prepareImage(data, "image/webp", "x.webp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if img.block.Width != 0 || img.block.Data != base64.StdEncoding.EncodeToString(data) {
		t.Error("undecodable image should pass through unchanged")
	}
	if _, err := prepareImage(data, "image/webp", "x.webp", []int{0, 0, 1, 1}); err == nil {
		t.Error("cropping an undecodable image should fail")
	}
}

func TestResizeAreaAveragesStripes(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 1))
	for x := 0; x < 4; x++ {
		v := uint8(0)
		if x%2 == 1 {
			v = 200
		}
		src.SetRGBA(x, 0, color.RGBA{v, v, v, 255})
	}
	dst := resizeArea(src, 2, 1)
	for x := 0; x < 2; x++ {
		if c := dst.RGBAAt(x, 0); c.R != 100 || c.A != 255 {
			t.Errorf("pixel %d = %v, want grey 100", x, c)
		}
	}
	odd := resizeArea(src, 3, 1)
	if c := odd.RGBAAt(1, 0); c.R != 100 {
		t.Errorf("middle of 4→3 = %v, want 100", c)
	}
}

func TestReadToolCropsImages(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "shot.png")
	if err := os.WriteFile(p, testPNG(t, 2000, 1000, solid(color.RGBA{0, 0, 0, 255})), 0o600); err != nil {
		t.Fatal(err)
	}
	res := readPath(p, 0, 0, []int{0, 0, 100, 50})
	if res.IsError || len(res.Images) != 1 || res.Images[0].Width != 100 {
		t.Fatalf("crop read: %+v", res.Text)
	}
	if res := readPath(p, 0, 0, nil); res.Images[0].Width != 1568 {
		t.Errorf("full read width %d", res.Images[0].Width)
	}
	txt := filepath.Join(dir, "a.txt")
	os.WriteFile(txt, []byte("x\n"), 0o600)
	if res := readPath(txt, 0, 0, []int{0, 0, 1, 1}); !res.IsError {
		t.Error("crop on a text file should fail")
	}
}

func TestMCPImagesGetAPath(t *testing.T) {
	env := &Env{Cwd: t.TempDir(), SpillDir: t.TempDir()}
	data := base64.StdEncoding.EncodeToString(testPNG(t, 3000, 1500, solid(color.RGBA{0, 0, 0, 255})))
	img, err := mcpImage(env, "image/png", data)
	if err != nil {
		t.Fatal(err)
	}
	src := img.block.Source
	if !strings.HasSuffix(src, ".png") || !strings.Contains(img.note, src) || !strings.Contains(img.note, "crop") {
		t.Fatalf("source %q note %q", src, img.note)
	}
	if res := readPath(src, 0, 0, []int{0, 0, 10, 10}); res.IsError || res.Images[0].Width != 10 {
		t.Errorf("saved MCP image can't be cropped: %s", res.Text)
	}
}

func TestPrepareImageUsesRealFormat(t *testing.T) {
	data := testPNG(t, 10, 10, solid(color.RGBA{0, 0, 0, 255}))
	img, err := prepareImage(data, "image/jpeg", "fake.jpg", nil)
	if err != nil || img.block.MediaType != "image/png" {
		t.Errorf("media type %q, err %v", img.block.MediaType, err)
	}
}
