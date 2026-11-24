package imgutil

import (
	"bytes"
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
	dst := Resize(src, src.Rect, 2, 1)
	for x := 0; x < 2; x++ {
		if c := dst.RGBAAt(x, 0); c.R != 100 || c.A != 255 {
			t.Errorf("pixel %d = %v, want grey 100", x, c)
		}
	}
	odd := Resize(src, src.Rect, 3, 1)
	if c := odd.RGBAAt(1, 0); c.R != 100 {
		t.Errorf("middle of 4→3 = %v, want 100", c)
	}
}

func TestResizeReadsAnyImageAndRegionRowByRow(t *testing.T) {
	src := image.NewNRGBA(image.Rect(5, 7, 5+61, 7+43))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 37)
	}
	region := image.Rect(3, 2, 57, 40)
	got := Resize(src, region, 19, 13)
	want := Resize(ToRGBA(src, region), image.Rect(0, 0, region.Dx(), region.Dy()), 19, 13)
	if !bytes.Equal(got.Pix, want.Pix) {
		t.Error("resizing an NRGBA region differs from resizing its RGBA copy")
	}
	if same := Resize(src, region, region.Dx(), region.Dy()); !bytes.Equal(same.Pix, ToRGBA(src, region).Pix) {
		t.Error("a same-size resize should be the region's RGBA copy")
	}
}
