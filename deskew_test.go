package imaging

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// makeTextDocument renders a deterministic multi-line "text" sample: rows of
// black word-like bars on a white background.
func makeTextDocument(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	black := color.NRGBA{0, 0, 0, 255}
	seed := uint32(12345)
	rnd := func() int {
		seed = seed*1664525 + 1013904223
		return int(seed >> 16)
	}
	for lineY := 30; lineY < h-40; lineY += 28 {
		x := 40
		for x < w-60 {
			wordW := 20 + rnd()%40
			for yy := lineY; yy < lineY+10; yy++ {
				for xx := x; xx < x+wordW && xx < w-40; xx++ {
					img.SetNRGBA(xx, yy, black)
				}
			}
			x += wordW + 12 + rnd()%10
		}
	}
	return img
}

func countDarkPixels(img *image.NRGBA) int {
	count := 0
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			i := y*img.Stride + x*4
			if img.Pix[i+3] > 128 && img.Pix[i+0] < 128 {
				count++
			}
		}
	}
	return count
}

func TestDeskewAngle(t *testing.T) {
	doc := makeTextDocument(400, 300)
	angles := []float64{-9.2, -5.7, -1.3, 1.0, 3.8, 8.4}
	for _, angle := range angles {
		skewed := Rotate(doc, angle, color.White)
		corrected, got, confidence := Deskew(skewed, &DeskewOptions{MaxAngle: 12})
		if math.Abs(got-angle) > 0.3 {
			t.Errorf("angle %v: got estimate %v", angle, got)
		}
		if confidence < 0.3 {
			t.Errorf("angle %v: got confidence %v", angle, confidence)
		}
		// Without Crop the result keeps the full rotated content and grows.
		if corrected.Bounds().Dx() < doc.Bounds().Dx() || corrected.Bounds().Dy() < doc.Bounds().Dy() {
			t.Errorf("angle %v: got cropped bounds %v", angle, corrected.Bounds())
		}
		// The text must not be cut off: the dark pixel count is preserved.
		want, gotDark := countDarkPixels(skewed), countDarkPixels(corrected)
		if math.Abs(float64(gotDark-want))/float64(want) > 0.2 {
			t.Errorf("angle %v: dark pixels got %d want ~%d", angle, gotDark, want)
		}
	}
}

func TestDeskewZeroAngle(t *testing.T) {
	doc := makeTextDocument(400, 300)
	corrected, angle, _ := Deskew(doc, nil)
	if angle != 0 {
		t.Fatalf("got angle %v want 0", angle)
	}
	// Zero angle: the pixels must not change.
	if !compareNRGBA(corrected, doc, 0) {
		t.Fatalf("zero angle changed the pixels")
	}
}

func TestDeskewCrop(t *testing.T) {
	doc := makeTextDocument(400, 300)
	skewed := Rotate(doc, 5, color.White)
	corrected, angle, _ := Deskew(skewed, &DeskewOptions{MaxAngle: 10, Crop: true})
	if math.Abs(angle-5) > 0.3 {
		t.Fatalf("got angle %v want ~5", angle)
	}
	if corrected.Bounds() != image.Rect(0, 0, skewed.Bounds().Dx(), skewed.Bounds().Dy()) {
		t.Fatalf("got bounds %v want original size %v", corrected.Bounds(), skewed.Bounds())
	}
}

func TestDeskewBlankPage(t *testing.T) {
	blank := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for i := range blank.Pix {
		blank.Pix[i] = 0xff
	}
	corrected, angle, confidence := Deskew(blank, nil)
	if angle != 0 || confidence != 0 {
		t.Fatalf("got angle %v confidence %v want 0, 0", angle, confidence)
	}
	if !compareNRGBA(corrected, blank, 0) {
		t.Fatalf("blank page was modified")
	}
}

func TestDeskewNearlySolid(t *testing.T) {
	// Smooth low-contrast gradient: no foreground, no rotation.
	img := image.NewNRGBA(image.Rect(0, 0, 300, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 300; x++ {
			v := uint8(180 + (x+y)%30)
			img.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
		}
	}
	corrected, angle, confidence := Deskew(img, nil)
	if angle != 0 || confidence != 0 {
		t.Fatalf("got angle %v confidence %v want 0, 0", angle, confidence)
	}
	if !compareNRGBA(corrected, img, 0) {
		t.Fatalf("nearly solid page was modified")
	}
}

func TestDeskewNoiseLines(t *testing.T) {
	// A blank page with a couple of thin slanted noise lines.
	img := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	black := color.NRGBA{0, 0, 0, 255}
	for x := 0; x < 400; x++ {
		img.SetNRGBA(x, 50+x/4, black)
		img.SetNRGBA(x, 200-x/5, black)
	}
	corrected, angle, confidence := Deskew(img, nil)
	if angle != 0 || confidence != 0 {
		t.Fatalf("got angle %v confidence %v want 0, 0", angle, confidence)
	}
	if !compareNRGBA(corrected, img, 0) {
		t.Fatalf("noise page was modified")
	}
}

func TestDeskewTinyImage(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := range img.Pix {
		img.Pix[i] = 0xff
	}
	img.SetNRGBA(1, 1, color.NRGBA{0, 0, 0, 255})
	corrected, angle, _ := Deskew(img, nil)
	if angle != 0 {
		t.Fatalf("got angle %v want 0", angle)
	}
	if !compareNRGBA(corrected, img, 0) {
		t.Fatalf("tiny image was modified")
	}
}

func TestDeskewInvalidMaxAngle(t *testing.T) {
	doc := makeTextDocument(200, 150)
	skewed := Rotate(doc, 5, color.White)
	for _, maxAngle := range []float64{0, -3, math.NaN(), math.Inf(1), 46, 100} {
		corrected, angle, confidence := Deskew(skewed, &DeskewOptions{MaxAngle: maxAngle})
		if angle != 0 || confidence != 0 {
			t.Errorf("maxAngle %v: got angle %v confidence %v want 0, 0", maxAngle, angle, confidence)
		}
		if !compareNRGBA(corrected, skewed, 0) {
			t.Errorf("maxAngle %v: image was modified", maxAngle)
		}
	}
}

func TestDeskewTransparentBackground(t *testing.T) {
	// Black text on a transparent background.
	doc := image.NewNRGBA(image.Rect(0, 0, 400, 300))
	black := color.NRGBA{0, 0, 0, 255}
	for lineY := 30; lineY < 260; lineY += 28 {
		for yy := lineY; yy < lineY+10; yy++ {
			for xx := 40; xx < 360; xx++ {
				doc.SetNRGBA(xx, yy, black)
			}
		}
	}
	skewed := Rotate(doc, 6, color.Transparent)
	corrected, angle, _ := Deskew(skewed, &DeskewOptions{MaxAngle: 10, Background: color.Transparent})
	if math.Abs(angle-6) > 0.3 {
		t.Fatalf("got angle %v want ~6", angle)
	}
	// The uncovered corner stays transparent.
	if a := corrected.NRGBAAt(0, 0).A; a != 0 {
		t.Fatalf("got corner alpha %d want 0", a)
	}
}

func TestDeskewNonZeroBounds(t *testing.T) {
	doc := makeTextDocument(400, 300)
	shifted := &image.NRGBA{
		Pix:    doc.Pix,
		Stride: doc.Stride,
		Rect:   image.Rect(11, 23, 411, 323),
	}
	skewed := Rotate(shifted, -4, color.White)
	corrected, angle, _ := Deskew(skewed, &DeskewOptions{MaxAngle: 10})
	if math.Abs(angle+4) > 0.3 {
		t.Fatalf("got angle %v want ~-4", angle)
	}
	if corrected.Bounds().Min != image.Pt(0, 0) {
		t.Fatalf("got non-zero bounds %v", corrected.Bounds())
	}
}

func TestDeskewDefaultOptions(t *testing.T) {
	doc := makeTextDocument(400, 300)
	skewed := Rotate(doc, 7.5, color.White)
	_, angle, confidence := Deskew(skewed, nil)
	if math.Abs(angle-7.5) > 0.3 {
		t.Fatalf("got angle %v want ~7.5", angle)
	}
	if confidence < 0.3 {
		t.Fatalf("got confidence %v", confidence)
	}
}
