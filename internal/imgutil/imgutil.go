package imgutil

import (
	"image"
	"image/draw"
	"math"
)

func ToRGBA(img image.Image, region image.Rectangle) *image.RGBA {
	b := region.Add(img.Bounds().Min)
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
	return dst
}

type areaSpan struct {
	start   int
	weights []float32
}

func areaSpans(src, dst int) []areaSpan {
	scale := float64(src) / float64(dst)
	out := make([]areaSpan, dst)
	for i := range out {
		a, b := float64(i)*scale, float64(i+1)*scale
		s, e := int(a), min(int(math.Ceil(b)), src)
		ws := make([]float32, e-s)
		for j := s; j < e; j++ {
			ws[j-s] = float32((math.Min(b, float64(j+1)) - math.Max(a, float64(j))) / scale)
		}
		out[i] = areaSpan{s, ws}
	}
	return out
}

func Resize(src *image.RGBA, w, h int) *image.RGBA {
	sw, sh := src.Rect.Dx(), src.Rect.Dy()
	if sw == w && sh == h {
		return src
	}
	tmp := make([]float32, w*sh*4)
	hs := areaSpans(sw, w)
	for y := 0; y < sh; y++ {
		row := src.Pix[y*src.Stride:]
		for x, sp := range hs {
			o := tmp[(y*w+x)*4:]
			for k, wt := range sp.weights {
				p := row[(sp.start+k)*4:]
				o[0] += wt * float32(p[0])
				o[1] += wt * float32(p[1])
				o[2] += wt * float32(p[2])
				o[3] += wt * float32(p[3])
			}
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y, sp := range areaSpans(sh, h) {
		for x := 0; x < w; x++ {
			var c [4]float32
			for k, wt := range sp.weights {
				p := tmp[((sp.start+k)*w+x)*4:]
				c[0] += wt * p[0]
				c[1] += wt * p[1]
				c[2] += wt * p[2]
				c[3] += wt * p[3]
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			for i := range c {
				o[i] = uint8(min(max(c[i]+0.5, 0), 255))
			}
		}
	}
	return dst
}
