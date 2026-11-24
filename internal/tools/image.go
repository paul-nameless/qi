package tools

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"sync"

	"github.com/paul-nameless/qi/internal/imgutil"
	"github.com/paul-nameless/qi/internal/llm"
)

const (
	maxImageEdge      = 1568
	maxImageSent      = 3_750_000
	maxImageFile      = 50 << 20
	maxImagePixels    = 40_000_000
	jpegFallbackBytes = 500 << 10
	jpegQuality       = 85
)

var decodeMu sync.Mutex

type preparedImage struct {
	block llm.Block
	note  string
}

func prepareImage(data []byte, mediaType, source string, crop []int) (preparedImage, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		if len(crop) > 0 {
			return preparedImage{}, fmt.Errorf("can't crop this image: %v", err)
		}
		if len(data) > maxImageSent {
			return preparedImage{}, fmt.Errorf("image is %d bytes and its format can't be resized (max %d)", len(data), maxImageSent)
		}
		return preparedImage{newImageBlock(data, mediaType, source, 0, 0), fmt.Sprintf("%s (%d bytes)", imageLabel(source), len(data))}, nil
	}
	w, h := cfg.Width, cfg.Height
	mediaType = "image/" + format
	region, err := cropRegion(crop, w, h)
	if err != nil {
		return preparedImage{}, err
	}
	if len(crop) == 0 && max(w, h) <= maxImageEdge && len(data) <= maxImageSent {
		return preparedImage{newImageBlock(data, mediaType, source, w, h), fmt.Sprintf("%s %d×%d", imageLabel(source), w, h)}, nil
	}
	if w*h > maxImagePixels {
		return preparedImage{}, fmt.Errorf("image is %d×%d, too large to decode (max %d pixels)", w, h, maxImagePixels)
	}
	decodeMu.Lock()
	defer decodeMu.Unlock()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return preparedImage{}, fmt.Errorf("decode image: %w", err)
	}
	dw, dh := fitEdge(region.Dx(), region.Dy(), maxImageEdge)
	small := imgutil.Resize(img, region, dw, dh)
	out, outType, err := encodeImage(small, format == "jpeg", len(crop) == 0)
	if err != nil {
		return preparedImage{}, err
	}
	if len(out) > maxImageSent {
		return preparedImage{}, fmt.Errorf("image is still %d bytes after resizing (max %d)", len(out), maxImageSent)
	}
	return preparedImage{newImageBlock(out, outType, source, dw, dh), imageNote(source, w, h, region, crop, dw, dh)}, nil
}

func cropRegion(crop []int, w, h int) (image.Rectangle, error) {
	if len(crop) == 0 {
		return image.Rect(0, 0, w, h), nil
	}
	if len(crop) != 4 || crop[0] < 0 || crop[1] < 0 || crop[2] <= 0 || crop[3] <= 0 {
		return image.Rectangle{}, errors.New("crop must be [x, y, width, height] with x, y ≥ 0 and a positive width and height")
	}
	x, y := crop[0], crop[1]
	if x >= w || y >= h {
		return image.Rectangle{}, fmt.Errorf("crop starts at %d,%d, outside the %d×%d image", x, y, w, h)
	}
	return image.Rect(x, y, x+min(crop[2], w-x), y+min(crop[3], h-y)), nil
}

func newImageBlock(data []byte, mediaType, source string, w, h int) llm.Block {
	return llm.Block{Type: llm.BlockImage, MediaType: mediaType, Data: base64.StdEncoding.EncodeToString(data), Width: w, Height: h, Source: source}
}

func imageNote(source string, w, h int, region image.Rectangle, crop []int, dw, dh int) string {
	head := fmt.Sprintf("%s %d×%d", imageLabel(source), w, h)
	if len(crop) > 0 {
		head = fmt.Sprintf("region x=%d y=%d %d×%d of %s (%d×%d)", region.Min.X, region.Min.Y, region.Dx(), region.Dy(), imageLabel(source), w, h)
	}
	if dw == region.Dx() && dh == region.Dy() {
		return head + ", full resolution"
	}
	k := float64(region.Dx()) / float64(dw)
	point := fmt.Sprintf("(x×%.4f, y×%.4f)", k, k)
	if len(crop) > 0 {
		point = fmt.Sprintf("(%d + x×%.4f, %d + y×%.4f)", region.Min.X, k, region.Min.Y, k)
	}
	note := fmt.Sprintf("%s, sent as %d×%d; a point (x, y) in it is %s in the original", head, dw, dh, point)
	if source != "" {
		note += "; pass crop [x, y, width, height] in original pixels to see a region at full resolution"
	}
	return note
}

func imageLabel(source string) string {
	if source == "" {
		return "image"
	}
	return "image " + source
}

func fitEdge(w, h, edge int) (int, int) {
	if w <= edge && h <= edge {
		return w, h
	}
	if w >= h {
		return edge, max(1, int(math.Round(float64(h)*float64(edge)/float64(w))))
	}
	return max(1, int(math.Round(float64(w)*float64(edge)/float64(h)))), edge
}

func encodeImage(img *image.RGBA, preferJPEG, allowJPEG bool) ([]byte, string, error) {
	var buf bytes.Buffer
	if preferJPEG {
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: jpegQuality}); err != nil {
			return nil, "", fmt.Errorf("encode jpeg: %w", err)
		}
		return buf.Bytes(), "image/jpeg", nil
	}
	if err := png.Encode(&buf, img); err != nil {
		return nil, "", fmt.Errorf("encode png: %w", err)
	}
	if !allowJPEG || buf.Len() <= jpegFallbackBytes || !img.Opaque() {
		return buf.Bytes(), "image/png", nil
	}
	var jb bytes.Buffer
	if err := jpeg.Encode(&jb, img, &jpeg.Options{Quality: jpegQuality}); err == nil && jb.Len() < buf.Len() {
		return jb.Bytes(), "image/jpeg", nil
	}
	return buf.Bytes(), "image/png", nil
}
