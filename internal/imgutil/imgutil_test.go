package imgutil

import (
	"image"
	"image/color"
	"testing"
)

func TestResizeAreaAveragesStripes(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 1))
	for x := 0; x < 4; x++ {
		v := uint8(0)
		if x%2 == 1 {
			v = 200
		}
		src.SetRGBA(x, 0, color.RGBA{v, v, v, 255})
	}
	dst := Resize(src, 2, 1)
	for x := 0; x < 2; x++ {
		if c := dst.RGBAAt(x, 0); c.R != 100 || c.A != 255 {
			t.Errorf("pixel %d = %v, want grey 100", x, c)
		}
	}
	odd := Resize(src, 3, 1)
	if c := odd.RGBAAt(1, 0); c.R != 100 {
		t.Errorf("middle of 4→3 = %v, want 100", c)
	}
}
