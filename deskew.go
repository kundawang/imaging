package imaging

import (
	"image"
	"image/color"
	"math"
)

// DeskewOptions configures the Deskew function.
type DeskewOptions struct {
	// MaxAngle is the maximum absolute skew angle to detect, in degrees.
	// It must be in the range (0, 45]. Any other value (including NaN and
	// Inf) is invalid: Deskew returns the image unchanged. When opts is nil,
	// the default of 15 degrees is used.
	MaxAngle float64

	// Crop controls the size of the corrected image. If false (default), the
	// whole image is rotated and the result grows to fit all the rotated
	// content, like the Rotate function does. If true, the rotated image is
	// cropped back to the original size, cutting off the corners that were
	// rotated out of the frame.
	Crop bool

	// Background is the color used to fill the areas uncovered by the
	// rotation. If nil, white is used. Use color.Transparent to keep the
	// uncovered areas transparent.
	Background color.Color

	// MinConfidence is the minimum confidence value (in the range 0..1)
	// required to apply the rotation. If it is <= 0, the default of 0.2 is
	// used.
	MinConfidence float64
}

// Deskew estimates the skew angle of a scanned document image and returns
// the corrected image together with the detected skew angle in degrees and
// a confidence value in the range 0..1.
//
// The skew is estimated by maximizing the variance of the horizontal
// projection profile of the foreground (content) pixels over the range
// [-MaxAngle, MaxAngle]. A positive angle means the content is rotated
// counter-clockwise (in the sense of the Rotate function); the returned
// image is rotated by -angle to straighten it.
//
// The returned image is an unmodified copy of the source in the following
// cases: the image is empty or very small, the page is blank or nearly a
// solid color, there are too few foreground pixels (e.g. only a couple of
// noise lines), the content does not form enough text-like rows,
// opts.MaxAngle is invalid, the detected angle is zero, or the confidence
// is below MinConfidence. In these cases no arbitrary rotation is applied;
// the reported angle and confidence are zero unless an estimate was found
// but rejected as not trustworthy.
//
// Images with non-zero Bounds are normalized before processing and the
// result always has zero-based bounds. Rotation uses the same bilinear
// interpolation as the Rotate function.
func Deskew(img image.Image, opts *DeskewOptions) (corrected *image.NRGBA, angle float64, confidence float64) {
	o := DeskewOptions{
		MaxAngle:      15,
		MinConfidence: 0.2,
		Background:    color.White,
	}
	if opts != nil {
		o = *opts
		if o.MinConfidence <= 0 {
			o.MinConfidence = 0.2
		}
		if o.Background == nil {
			o.Background = color.White
		}
	}

	unchanged := func() (*image.NRGBA, float64, float64) {
		return Clone(img), 0, 0
	}

	if o.MaxAngle <= 0 || o.MaxAngle > 45 || math.IsNaN(o.MaxAngle) || math.IsInf(o.MaxAngle, 0) {
		return unchanged()
	}

	src := toNRGBA(img)
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if w < 16 || h < 16 {
		return unchanged()
	}

	// Bound the number of analyzed pixels by sampling with a stride.
	step := 1
	for (w/step)*(h/step) > 250000 {
		step++
	}

	// Find the dominant background luminance. Pixels are composited over
	// white, so fully transparent areas count as white background.
	var hist [256]int
	total := 0
	for y := 0; y < h; y += step {
		for x := 0; x < w; x += step {
			i := y*src.Stride + x*4
			a := uint32(src.Pix[i+3])
			r := (uint32(src.Pix[i+0])*a + 255*(255-a)) / 255
			g := (uint32(src.Pix[i+1])*a + 255*(255-a)) / 255
			b := (uint32(src.Pix[i+2])*a + 255*(255-a)) / 255
			lum := (19595*r + 38470*g + 7471*b) >> 16
			hist[lum]++
			total++
		}
	}
	bgLum := 0
	for i := 1; i < 256; i++ {
		if hist[i] > hist[bgLum] {
			bgLum = i
		}
	}

	// Collect the foreground pixels: the ones that differ from the dominant
	// background level.
	const lumThreshold = 40
	var fx, fy []float64
	cx := float64(w-1) / 2
	cy := float64(h-1) / 2
	for y := 0; y < h; y += step {
		for x := 0; x < w; x += step {
			i := y*src.Stride + x*4
			a := uint32(src.Pix[i+3])
			r := (uint32(src.Pix[i+0])*a + 255*(255-a)) / 255
			g := (uint32(src.Pix[i+1])*a + 255*(255-a)) / 255
			b := (uint32(src.Pix[i+2])*a + 255*(255-a)) / 255
			lum := int((19595*r + 38470*g + 7471*b) >> 16)
			d := lum - bgLum
			if d < 0 {
				d = -d
			}
			if d > lumThreshold {
				fx = append(fx, float64(x)-cx)
				fy = append(fy, float64(y)-cy)
			}
		}
	}

	// Reject blank or nearly solid pages and pages with only a few noise
	// pixels: not enough foreground to estimate anything reliably.
	minForeground := total / 500
	if minForeground < 100 {
		minForeground = 100
	}
	if len(fx) < minForeground {
		return unchanged()
	}

	// Projection profile score: variance of the row histogram of the
	// foreground points rotated by the candidate angle.
	off := (w+h)/2 + 1
	bins := make([]int, 2*off+1)
	score := func(sin, cos float64) float64 {
		for i := range bins {
			bins[i] = 0
		}
		for j := range fx {
			y := fx[j]*sin + fy[j]*cos
			bins[int(y)+off]++
		}
		var sum, sumsq float64
		for _, c := range bins {
			sum += float64(c)
			sumsq += float64(c * c)
		}
		n := float64(len(bins))
		return sumsq/n - (sum/n)*(sum/n)
	}

	// Coarse scan over the full range, then a fine scan around the best
	// coarse angle.
	bestAngle, bestScore := 0.0, -1.0
	var coarseSum float64
	coarseCount := 0
	for a := -o.MaxAngle; a <= o.MaxAngle+1e-9; a += 0.5 {
		sin, cos := math.Sincos(a * math.Pi / 180)
		s := score(sin, cos)
		coarseSum += s
		coarseCount++
		if s > bestScore {
			bestScore, bestAngle = s, a
		}
	}
	lo := math.Max(bestAngle-0.6, -o.MaxAngle)
	hi := math.Min(bestAngle+0.6, o.MaxAngle)
	for a := lo; a <= hi+1e-9; a += 0.05 {
		sin, cos := math.Sincos(a * math.Pi / 180)
		if s := score(sin, cos); s > bestScore {
			bestScore, bestAngle = s, a
		}
	}

	if bestScore <= 0 {
		return unchanged()
	}

	// Confidence: how much the best score stands out from the average.
	confidence = (bestScore - coarseSum/float64(coarseCount)) / bestScore
	if confidence < 0 {
		confidence = 0
	}
	if confidence > 1 {
		confidence = 1
	}

	// The content must form enough text-like rows. This rejects pages whose
	// foreground is just a few slanted noise lines: those produce only rows
	// with a pixel or two each.
	sin, cos := math.Sincos(bestAngle * math.Pi / 180)
	for i := range bins {
		bins[i] = 0
	}
	maxRow := 0
	for j := range fx {
		y := fx[j]*sin + fy[j]*cos
		b := int(y) + off
		bins[b]++
		if bins[b] > maxRow {
			maxRow = bins[b]
		}
	}
	strongThreshold := maxRow / 10
	if strongThreshold < 5 {
		strongThreshold = 5
	}
	strongRows := 0
	for _, c := range bins {
		if c >= strongThreshold {
			strongRows++
		}
	}
	if strongRows < 8 {
		return unchanged()
	}

	angle = bestAngle
	if math.Abs(angle) < 0.05 {
		// Zero angle: the pixels must not change.
		return Clone(img), 0, confidence
	}
	if confidence < o.MinConfidence {
		return Clone(img), angle, confidence
	}

	corrected = Rotate(src, -angle, o.Background)
	if o.Crop {
		corrected = CropCenter(corrected, w, h)
	}
	return corrected, angle, confidence
}
