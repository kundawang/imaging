package imaging

import (
	"errors"
	"image"
	"image/color"
)

// ErrNoOverlap is returned by RegisterTranslation when no translation within
// the search range produces enough common valid (non-transparent) pixels.
var ErrNoOverlap = errors.New("imaging: not enough overlap between the images")

// RegisterTranslation finds the integer translation that best aligns img with
// ref and returns it together with a match score.
//
// The returned offset (dx, dy) is the translation that must be applied to img
// to align it with ref: after the translation, img pixel (x-dx, y-dy)
// corresponds to ref pixel (x, y). Both images are compared in their local
// coordinates starting at (0, 0), regardless of their Bounds().Min.
//
// The function evaluates every integer translation with |dx| and |dy| bounded
// by maxShift (the range is additionally clamped to translations where the two
// images intersect at all, so an arbitrarily large maxShift is safe). For each
// translation the mean absolute luminance difference is computed over the
// common valid pixels: a pixel pair contributes only when both pixels are
// non-transparent, and its contribution is weighted by the product of the two
// alpha values, so fully transparent pixels contribute no error. The score is
// 1.0 for a perfect match and decreases towards 0.0 as the mean luminance
// difference grows.
//
// minOverlap (in the range (0, 1]) is the minimum required ratio of common
// valid pixels to the number of valid pixels of the image that has fewer of
// them. If no translation satisfies this requirement (including the case of a
// fully transparent image), ErrNoOverlap is returned.
//
// Ties are broken deterministically: among translations with equal score the
// one with the smallest |dx|+|dy| wins; remaining ties are broken by the
// smallest dy, then the smallest dx. In particular, ambiguous (e.g. solid
// color) images yield the zero translation.
func RegisterTranslation(ref, img image.Image, maxShift int, minOverlap float64) (offset image.Point, score float64, err error) {
	if maxShift < 0 {
		return image.Point{}, 0, errors.New("imaging: maxShift must be >= 0")
	}
	if minOverlap <= 0 || minOverlap > 1 {
		return image.Point{}, 0, errors.New("imaging: minOverlap must be in the range (0, 1]")
	}

	a := toNRGBA(ref)
	b := toNRGBA(img)
	aw, ah := a.Bounds().Dx(), a.Bounds().Dy()
	bw, bh := b.Bounds().Dx(), b.Bounds().Dy()

	validA := countValidPixels(a)
	validB := countValidPixels(b)
	if validA == 0 || validB == 0 {
		return image.Point{}, 0, ErrNoOverlap
	}
	minValid := validA
	if validB < minValid {
		minValid = validB
	}

	// Clamp the search range to translations where the images intersect.
	dxLo, dxHi := -(bw - 1), aw-1
	if dxLo < -maxShift {
		dxLo = -maxShift
	}
	if dxHi > maxShift {
		dxHi = maxShift
	}
	dyLo, dyHi := -(bh - 1), ah-1
	if dyLo < -maxShift {
		dyLo = -maxShift
	}
	if dyHi > maxShift {
		dyHi = maxShift
	}

	type match struct {
		dx, dy int
		score  float64
		dist   int
		ok     bool
	}

	rows := make([]match, dyHi-dyLo+1)
	parallel(0, len(rows), func(is <-chan int) {
		for i := range is {
			dy := dyLo + i
			best := match{score: -1}
			for dx := dxLo; dx <= dxHi; dx++ {
				score, ok := translationScore(a, b, dx, dy, minOverlap, minValid)
				if !ok {
					continue
				}
				dist := absint(dx) + absint(dy)
				if !best.ok || score > best.score || (score == best.score && dist < best.dist) {
					best = match{dx: dx, dy: dy, score: score, dist: dist, ok: true}
				}
			}
			rows[i] = best
		}
	})

	best := match{score: -1}
	for _, m := range rows {
		if !m.ok {
			continue
		}
		if !best.ok || m.score > best.score || (m.score == best.score && m.dist < best.dist) {
			best = m
		}
	}
	if !best.ok {
		return image.Point{}, 0, ErrNoOverlap
	}
	return image.Pt(best.dx, best.dy), best.score, nil
}

// Align returns img translated by the given offset on a transparent canvas
// the same size as ref. The offset convention matches RegisterTranslation:
// the result can be composited with ref at position (0, 0), e.g. using
// Overlay, without the ghosting caused by misalignment.
func Align(ref, img image.Image, offset image.Point) *image.NRGBA {
	b := ref.Bounds()
	dst := New(b.Dx(), b.Dy(), color.NRGBA{})
	return Paste(dst, img, offset)
}

// countValidPixels returns the number of pixels with non-zero alpha.
func countValidPixels(img *image.NRGBA) int {
	count := 0
	for i := 3; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 {
			count++
		}
	}
	return count
}

// translationScore computes the match score for a single translation (dx, dy)
// of image b relative to image a. It returns ok=false when the common valid
// area does not satisfy the minOverlap requirement.
func translationScore(a, b *image.NRGBA, dx, dy int, minOverlap float64, minValid int) (score float64, ok bool) {
	aw, ah := a.Bounds().Dx(), a.Bounds().Dy()
	bw, bh := b.Bounds().Dx(), b.Bounds().Dy()

	x0, y0 := dx, dy
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	x1, y1 := bw+dx, bh+dy
	if x1 > aw {
		x1 = aw
	}
	if y1 > ah {
		y1 = ah
	}
	if x0 >= x1 || y0 >= y1 {
		return 0, false
	}

	var errSum, weightSum float64
	count := 0
	for y := y0; y < y1; y++ {
		ai := y*a.Stride + x0*4
		bi := (y-dy)*b.Stride + (x0-dx)*4
		for x := x0; x < x1; x++ {
			aa := a.Pix[ai+3]
			ba := b.Pix[bi+3]
			if aa != 0 && ba != 0 {
				la := 0.299*float64(a.Pix[ai]) + 0.587*float64(a.Pix[ai+1]) + 0.114*float64(a.Pix[ai+2])
				lb := 0.299*float64(b.Pix[bi]) + 0.587*float64(b.Pix[bi+1]) + 0.114*float64(b.Pix[bi+2])
				d := la - lb
				if d < 0 {
					d = -d
				}
				w := float64(aa) * float64(ba) / 0xff
				errSum += w * d
				weightSum += w
				count++
			}
			ai += 4
			bi += 4
		}
	}

	if weightSum == 0 || float64(count) < minOverlap*float64(minValid) {
		return 0, false
	}
	return 1 - (errSum/weightSum)/0xff, true
}
