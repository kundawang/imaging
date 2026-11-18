package imaging

import (
	"image"
	"image/color"
	"math"
	"testing"
)

// testPattern returns a fully opaque w x h image with a deterministic
// non-repetitive pattern.
func testPattern(w, h int) *image.NRGBA {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := img.PixOffset(x, y)
			img.Pix[i+0] = uint8((x*47 + y*11 + 13) % 256)
			img.Pix[i+1] = uint8((x*7 + y*31 + x*y*3 + 91) % 256)
			img.Pix[i+2] = uint8((x*x*5 + y*17 + 200) % 256)
			img.Pix[i+3] = 0xff
		}
	}
	return img
}

func TestRegisterTranslationKnownShift(t *testing.T) {
	ref := testPattern(16, 16)
	// img is ref translated by (-3, 2): the aligning offset is (3, -2).
	img := Paste(New(16, 16, color.NRGBA{}), ref, image.Pt(-3, 2))

	offset, score, err := RegisterTranslation(ref, img, 8, 0.5)
	if err != nil {
		t.Fatalf("got error %v", err)
	}
	if offset != image.Pt(3, -2) {
		t.Fatalf("got offset %v want %v", offset, image.Pt(3, -2))
	}
	if score != 1.0 {
		t.Fatalf("got score %v want 1.0", score)
	}

	// The aligned image equals ref on the common valid region and is
	// transparent elsewhere.
	got := Align(ref, img, offset)
	want := Paste(New(16, 16, color.NRGBA{}), Crop(ref, image.Rect(3, 0, 16, 14)), image.Pt(3, 0))
	if !compareNRGBA(got, want, 0) {
		t.Fatalf("got aligned image %#v want %#v", got, want)
	}
}

func TestRegisterTranslationTransparentHole(t *testing.T) {
	ref := testPattern(16, 16)
	img := Paste(New(16, 16, color.NRGBA{}), ref, image.Pt(-3, 2))

	// Cut a fully transparent hole into img: these pixels must not
	// contribute any error, so the score stays 1.0.
	hole := Clone(img)
	for y := 5; y < 9; y++ {
		for x := 5; x < 9; x++ {
			i := hole.PixOffset(x, y)
			hole.Pix[i+0] = 0
			hole.Pix[i+1] = 0
			hole.Pix[i+2] = 0
			hole.Pix[i+3] = 0
		}
	}

	offset, score, err := RegisterTranslation(ref, hole, 8, 0.5)
	if err != nil {
		t.Fatalf("got error %v", err)
	}
	if offset != image.Pt(3, -2) {
		t.Fatalf("got offset %v want %v", offset, image.Pt(3, -2))
	}
	if score != 1.0 {
		t.Fatalf("got score %v want 1.0", score)
	}
}

func TestRegisterTranslationSemiTransparentOcclusion(t *testing.T) {
	ref := testPattern(16, 16)
	img := Paste(New(16, 16, color.NRGBA{}), ref, image.Pt(-3, 2))

	// Occlude a region of img with a semi-transparent red layer.
	layer := New(5, 5, color.NRGBA{0xff, 0x00, 0x00, 0x80})
	occluded := Overlay(img, layer, image.Pt(4, 4), 1.0)

	offset, score, err := RegisterTranslation(ref, occluded, 8, 0.5)
	if err != nil {
		t.Fatalf("got error %v", err)
	}
	if offset != image.Pt(3, -2) {
		t.Fatalf("got offset %v want %v", offset, image.Pt(3, -2))
	}
	if score <= 0.5 || score >= 1.0 {
		t.Fatalf("got score %v want a value in (0.5, 1.0)", score)
	}
}

func TestRegisterTranslationDifferentSizes(t *testing.T) {
	ref := testPattern(20, 20)
	img := Crop(ref, image.Rect(4, 5, 14, 15))

	offset, score, err := RegisterTranslation(ref, img, 10, 1.0)
	if err != nil {
		t.Fatalf("got error %v", err)
	}
	if offset != image.Pt(4, 5) {
		t.Fatalf("got offset %v want %v", offset, image.Pt(4, 5))
	}
	if score != 1.0 {
		t.Fatalf("got score %v want 1.0", score)
	}
}

func TestRegisterTranslationNonZeroBounds(t *testing.T) {
	refBase := testPattern(16, 16)
	ref := &image.NRGBA{
		Pix:    refBase.Pix,
		Stride: refBase.Stride,
		Rect:   image.Rect(5, 7, 21, 23),
	}
	imgBase := Paste(New(16, 16, color.NRGBA{}), refBase, image.Pt(-3, 2))
	img := &image.NRGBA{
		Pix:    imgBase.Pix,
		Stride: imgBase.Stride,
		Rect:   image.Rect(-4, 2, 12, 18),
	}

	offset, score, err := RegisterTranslation(ref, img, 8, 0.5)
	if err != nil {
		t.Fatalf("got error %v", err)
	}
	if offset != image.Pt(3, -2) {
		t.Fatalf("got offset %v want %v", offset, image.Pt(3, -2))
	}
	if score != 1.0 {
		t.Fatalf("got score %v want 1.0", score)
	}
}

func TestRegisterTranslationSolidColor(t *testing.T) {
	testCases := []struct {
		name      string
		ref, img  image.Image
		wantScore float64
	}{
		{
			"same color",
			New(8, 8, color.NRGBA{0x80, 0x80, 0x80, 0xff}),
			New(8, 8, color.NRGBA{0x80, 0x80, 0x80, 0xff}),
			1.0,
		},
		{
			"different colors",
			New(8, 8, color.NRGBA{0x64, 0x64, 0x64, 0xff}),
			New(8, 8, color.NRGBA{0xc8, 0xc8, 0xc8, 0xff}),
			1 - 100.0/255.0,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			offset, score, err := RegisterTranslation(tc.ref, tc.img, 4, 0.5)
			if err != nil {
				t.Fatalf("got error %v", err)
			}
			// Ambiguous matches are resolved deterministically to the
			// zero translation.
			if offset != image.Pt(0, 0) {
				t.Fatalf("got offset %v want %v", offset, image.Pt(0, 0))
			}
			if math.Abs(score-tc.wantScore) > 1e-6 {
				t.Fatalf("got score %v want %v", score, tc.wantScore)
			}
		})
	}
}

func TestRegisterTranslationNoOverlap(t *testing.T) {
	ref := testPattern(8, 8)

	// Opaque left half vs opaque right half with maxShift=0:
	// no common valid pixels.
	left := New(8, 8, color.NRGBA{})
	for y := 0; y < 8; y++ {
		for x := 0; x < 4; x++ {
			i := left.PixOffset(x, y)
			left.Pix[i+0] = 0xff
			left.Pix[i+3] = 0xff
		}
	}
	right := New(8, 8, color.NRGBA{})
	for y := 0; y < 8; y++ {
		for x := 4; x < 8; x++ {
			i := right.PixOffset(x, y)
			right.Pix[i+0] = 0xff
			right.Pix[i+3] = 0xff
		}
	}

	// ref has one transparent pixel inside the 6x6 region matched by img,
	// so the overlap ratio 35/36 is below minOverlap=1.0.
	damaged := testPattern(8, 8)
	i := damaged.PixOffset(2, 2)
	damaged.Pix[i+3] = 0

	testCases := []struct {
		name       string
		ref, img   image.Image
		maxShift   int
		minOverlap float64
	}{
		{"fully transparent img", ref, New(8, 8, color.NRGBA{}), 4, 0.5},
		{"fully transparent ref", New(8, 8, color.NRGBA{}), ref, 4, 0.5},
		{"no common valid pixels", left, right, 0, 0.5},
		{"minOverlap not reached", damaged, testPattern(6, 6), 4, 1.0},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := RegisterTranslation(tc.ref, tc.img, tc.maxShift, tc.minOverlap)
			if err != ErrNoOverlap {
				t.Fatalf("got error %v want %v", err, ErrNoOverlap)
			}
		})
	}
}

func TestRegisterTranslationHugeSearchWindow(t *testing.T) {
	ref := testPattern(8, 8)
	img := Paste(New(8, 8, color.NRGBA{}), ref, image.Pt(2, 1))

	offset, score, err := RegisterTranslation(ref, img, math.MaxInt32, 0.5)
	if err != nil {
		t.Fatalf("got error %v", err)
	}
	if offset != image.Pt(-2, -1) {
		t.Fatalf("got offset %v want %v", offset, image.Pt(-2, -1))
	}
	if score != 1.0 {
		t.Fatalf("got score %v want 1.0", score)
	}
}

func TestRegisterTranslationInvalidParams(t *testing.T) {
	ref := testPattern(8, 8)
	testCases := []struct {
		name       string
		maxShift   int
		minOverlap float64
	}{
		{"negative maxShift", -1, 0.5},
		{"zero minOverlap", 4, 0},
		{"negative minOverlap", 4, -0.5},
		{"minOverlap above 1", 4, 1.5},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := RegisterTranslation(ref, ref, tc.maxShift, tc.minOverlap)
			if err == nil || err == ErrNoOverlap {
				t.Fatalf("got error %v want a parameter error", err)
			}
		})
	}
}

func TestAlign(t *testing.T) {
	red := New(2, 2, color.NRGBA{0xff, 0x00, 0x00, 0xff})
	testCases := []struct {
		name   string
		offset image.Point
		want   *image.NRGBA
	}{
		{
			"inside",
			image.Pt(1, 1),
			&image.NRGBA{
				Rect:   image.Rect(0, 0, 4, 4),
				Stride: 4 * 4,
				Pix: []uint8{
					0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0xff, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0xff, 0x00, 0x00, 0xff, 0xff, 0x00, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				},
			},
		},
		{
			"clipped",
			image.Pt(-1, -1),
			&image.NRGBA{
				Rect:   image.Rect(0, 0, 4, 4),
				Stride: 4 * 4,
				Pix: []uint8{
					0xff, 0x00, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
					0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
				},
			},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ref := New(4, 4, color.NRGBA{})
			got := Align(ref, red, tc.offset)
			if !compareNRGBA(got, tc.want, 0) {
				t.Fatalf("got result %#v want %#v", got, tc.want)
			}
		})
	}
}

func BenchmarkRegisterTranslation(b *testing.B) {
	ref := Clone(testdataFlowersSmallPNG)
	img := Paste(New(ref.Bounds().Dx(), ref.Bounds().Dy(), color.NRGBA{}), ref, image.Pt(-3, 2))
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		RegisterTranslation(ref, img, 10, 0.5)
	}
}
