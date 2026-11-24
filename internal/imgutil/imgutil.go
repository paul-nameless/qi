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

func Resize(img image.Image, region image.Rectangle, w, h int) *image.RGBA {
	b := region.Add(img.Bounds().Min)
	sw, sh := b.Dx(), b.Dy()
	if sw == w && sh == h {
		return ToRGBA(img, region)
	}
	hs, vs := areaSpans(sw, w), areaSpans(sh, h)
	row := image.NewRGBA(image.Rect(0, 0, sw, 1))
	hrow := make([]float32, w*4)
	acc := map[int][]float32{}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	y0 := 0
	for sy := 0; sy < sh; sy++ {
		draw.Draw(row, row.Rect, img, image.Pt(b.Min.X, b.Min.Y+sy), draw.Src)
		clear(hrow)
		for x, sp := range hs {
			o := hrow[x*4:]
			for k, wt := range sp.weights {
				p := row.Pix[(sp.start+k)*4:]
				o[0] += wt * float32(p[0])
				o[1] += wt * float32(p[1])
				o[2] += wt * float32(p[2])
				o[3] += wt * float32(p[3])
			}
		}
		for y := y0; y < h && vs[y].start <= sy; y++ {
			k := sy - vs[y].start
			if k >= len(vs[y].weights) {
				continue
			}
			a, ok := acc[y]
			if !ok {
				a = make([]float32, w*4)
				acc[y] = a
			}
			wt := vs[y].weights[k]
			for i, v := range hrow {
				a[i] += wt * v
			}
		}
		for y0 < h && vs[y0].start+len(vs[y0].weights) <= sy+1 {
			o := dst.Pix[y0*dst.Stride:]
			for i, v := range acc[y0] {
				o[i] = uint8(min(max(v+0.5, 0), 255))
			}
			delete(acc, y0)
			y0++
		}
	}
	return dst
}
