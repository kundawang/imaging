package imaging

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func openTestImage(t *testing.T, path string) image.Image {
	t.Helper()
	img, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	return img
}

func mustHash(t *testing.T, img image.Image) Fingerprint {
	t.Helper()
	fp, err := HashImage(img, color.White)
	if err != nil {
		t.Fatalf("HashImage: %v", err)
	}
	return fp
}

func jpegRoundTrip(t *testing.T, img image.Image, quality int) image.Image {
	t.Helper()
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatalf("jpeg.Encode: %v", err)
	}
	out, err := jpeg.Decode(&buf)
	if err != nil {
		t.Fatalf("jpeg.Decode: %v", err)
	}
	return out
}

func TestHashImageDeterministic(t *testing.T) {
	img := openTestImage(t, "testdata/branches.jpg")
	a := mustHash(t, img)
	b := mustHash(t, img)
	if a != b {
		t.Fatalf("hash not deterministic: %x != %x", uint64(a), uint64(b))
	}
	if Distance(a, b) != 0 {
		t.Fatalf("distance of identical hashes: got %d, want 0", Distance(a, b))
	}
}

func TestHashImageScaledAndCompressed(t *testing.T) {
	img := openTestImage(t, "testdata/branches.jpg")
	base := mustHash(t, img)

	downscaled := Resize(img, 100, 0, Lanczos)
	if d := Distance(base, mustHash(t, downscaled)); d > 8 {
		t.Fatalf("distance to downscaled copy: got %d, want <= 8", d)
	}

	upscaled := Resize(img, 800, 0, Lanczos)
	if d := Distance(base, mustHash(t, upscaled)); d > 8 {
		t.Fatalf("distance to upscaled copy: got %d, want <= 8", d)
	}

	recompressed := jpegRoundTrip(t, img, 50)
	if d := Distance(base, mustHash(t, recompressed)); d > 8 {
		t.Fatalf("distance to recompressed copy: got %d, want <= 8", d)
	}
}

func TestHashImageDistanceOrdering(t *testing.T) {
	img := openTestImage(t, "testdata/branches.jpg")
	other := openTestImage(t, "testdata/flowers.png")
	base := mustHash(t, img)

	copyDist := Distance(base, mustHash(t, Resize(img, 120, 0, Linear)))
	otherDist := Distance(base, mustHash(t, other))
	if copyDist >= otherDist {
		t.Fatalf("distance ordering: copy %d, different image %d; want copy < different", copyDist, otherDist)
	}
}

func TestHashImageTransparentBackground(t *testing.T) {
	// Semi-transparent red over white should hash the same as the
	// equivalent opaque composite.
	rgba := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			if x < 16 {
				rgba.SetNRGBA(x, y, color.NRGBA{0xff, 0x00, 0x00, 0x80})
			} else {
				rgba.SetNRGBA(x, y, color.NRGBA{0x00, 0x00, 0xff, 0xff})
			}
		}
	}
	opaque := Paste(New(32, 32, color.White), rgba, image.Point{})

	a := mustHash(t, rgba)
	b := mustHash(t, opaque)
	if a != b {
		t.Fatalf("transparent image hash %x != manual composite hash %x", uint64(a), uint64(b))
	}

	// A different background may produce a different fingerprint.
	fpBlack, err := HashImage(rgba, color.Black)
	if err != nil {
		t.Fatalf("HashImage with black background: %v", err)
	}
	_ = fpBlack // just ensure it computes without error
}

func TestHashImageEmpty(t *testing.T) {
	if _, err := HashImage(nil, color.White); err != ErrEmptyImage {
		t.Fatalf("nil image: got %v, want ErrEmptyImage", err)
	}
	empty := image.NewNRGBA(image.Rectangle{})
	if _, err := HashImage(empty, color.White); err != ErrEmptyImage {
		t.Fatalf("empty bounds: got %v, want ErrEmptyImage", err)
	}
}

func TestHashImageTiny(t *testing.T) {
	tiny := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	tiny.SetNRGBA(0, 0, color.NRGBA{0x80, 0x80, 0x80, 0xff})
	if _, err := HashImage(tiny, color.White); err != nil {
		t.Fatalf("1x1 image: %v", err)
	}
}

func TestHashImageConstant(t *testing.T) {
	constant := New(64, 64, color.NRGBA{0x40, 0x80, 0xc0, 0xff})
	fp := mustHash(t, constant)
	if fp != 0 {
		t.Fatalf("constant image hash: got %x, want 0", uint64(fp))
	}
}

func TestHashIndexAddRemove(t *testing.T) {
	ix, err := NewHashIndex(color.White, 3)
	if err != nil {
		t.Fatalf("NewHashIndex: %v", err)
	}
	img := openTestImage(t, "testdata/branches.jpg")

	if err := ix.Add("a", img); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if err := ix.Add("a", img); err != ErrDuplicateID {
		t.Fatalf("duplicate Add: got %v, want ErrDuplicateID", err)
	}
	if ix.Len() != 1 {
		t.Fatalf("Len: got %d, want 1", ix.Len())
	}

	other := openTestImage(t, "testdata/flowers.png")
	if err := ix.Add("b", other); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	if err := ix.Add("c", other); err != nil {
		t.Fatalf("Add c: %v", err)
	}
	if err := ix.Add("d", other); err != ErrIndexFull {
		t.Fatalf("Add beyond capacity: got %v, want ErrIndexFull", err)
	}

	if ix.Remove("missing") {
		t.Fatal("Remove of missing ID: got true, want false")
	}
	if !ix.Remove("b") {
		t.Fatal("Remove of existing ID: got false, want true")
	}
	if ix.Len() != 2 {
		t.Fatalf("Len after remove: got %d, want 2", ix.Len())
	}
	// Freed slot can be reused.
	if err := ix.Add("d", other); err != nil {
		t.Fatalf("Add after remove: %v", err)
	}
}

func TestHashIndexQuery(t *testing.T) {
	ix, err := NewHashIndex(color.White, 10)
	if err != nil {
		t.Fatalf("NewHashIndex: %v", err)
	}
	img := openTestImage(t, "testdata/branches.jpg")
	other := openTestImage(t, "testdata/flowers.png")

	// "orig" and "dup" share the same fingerprint; "small" is a scaled
	// copy; "other" is a different image.
	for _, tc := range []struct {
		id  string
		img image.Image
	}{
		{"orig", img},
		{"other", other},
		{"small", Resize(img, 120, 0, Lanczos)},
		{"dup", img},
	} {
		if err := ix.Add(tc.id, tc.img); err != nil {
			t.Fatalf("Add %q: %v", tc.id, err)
		}
	}

	matches, err := ix.Query(img, FingerprintBits, 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(matches) != 4 {
		t.Fatalf("Query returned %d matches, want 4", len(matches))
	}
	// Exact matches (including the scaled copy, which hashes identically)
	// come first in insertion order; the different image is last.
	wantOrder := []string{"orig", "small", "dup", "other"}
	for i, id := range wantOrder {
		if matches[i].ID != id {
			t.Fatalf("match %d: got %q (dist %d), want %q; all: %+v", i, matches[i].ID, matches[i].Distance, id, matches)
		}
	}
	for i := 1; i < len(matches); i++ {
		if matches[i-1].Distance > matches[i].Distance {
			t.Fatalf("matches not sorted by distance: %+v", matches)
		}
	}

	// Threshold filters out the different image.
	matches, err = ix.Query(img, 8, 0)
	if err != nil {
		t.Fatalf("Query with threshold: %v", err)
	}
	for _, m := range matches {
		if m.ID == "other" {
			t.Fatalf("threshold 8 should exclude different image: %+v", matches)
		}
	}

	// Limit caps the number of results.
	matches, err = ix.Query(img, FingerprintBits, 2)
	if err != nil {
		t.Fatalf("Query with limit: %v", err)
	}
	if len(matches) != 2 || matches[0].ID != "orig" || matches[1].ID != "small" {
		t.Fatalf("limited query: got %+v", matches)
	}

	// After removal the match disappears.
	if !ix.Remove("dup") {
		t.Fatal("Remove dup: got false, want true")
	}
	matches, err = ix.Query(img, 0, 0)
	if err != nil {
		t.Fatalf("Query after remove: %v", err)
	}
	if len(matches) != 2 || matches[0].ID != "orig" || matches[1].ID != "small" {
		t.Fatalf("exact query after remove: got %+v", matches)
	}
}

func TestHashIndexQueryInvalidDistance(t *testing.T) {
	ix, err := NewHashIndex(color.White, 1)
	if err != nil {
		t.Fatalf("NewHashIndex: %v", err)
	}
	img := New(8, 8, color.White)
	for _, d := range []int{-1, FingerprintBits + 1, 1000} {
		if _, err := ix.Query(img, d, 0); err != ErrInvalidDistance {
			t.Fatalf("maxDistance %d: got %v, want ErrInvalidDistance", d, err)
		}
		if _, err := ix.QueryFingerprint(0, d, 0); err != ErrInvalidDistance {
			t.Fatalf("QueryFingerprint maxDistance %d: got %v, want ErrInvalidDistance", d, err)
		}
	}
}

func TestHashIndexEdgeCases(t *testing.T) {
	if _, err := NewHashIndex(color.White, 0); err != ErrInvalidCapacity {
		t.Fatalf("capacity 0: got %v, want ErrInvalidCapacity", err)
	}
	if _, err := NewHashIndex(color.White, -5); err != ErrInvalidCapacity {
		t.Fatalf("capacity -5: got %v, want ErrInvalidCapacity", err)
	}

	ix, err := NewHashIndex(color.White, 2)
	if err != nil {
		t.Fatalf("NewHashIndex: %v", err)
	}
	if err := ix.Add("empty", image.NewNRGBA(image.Rectangle{})); err != ErrEmptyImage {
		t.Fatalf("Add empty image: got %v, want ErrEmptyImage", err)
	}
	if _, err := ix.Query(nil, 0, 0); err != ErrEmptyImage {
		t.Fatalf("Query nil image: got %v, want ErrEmptyImage", err)
	}

	// Querying an empty index yields no matches, not an error.
	matches, err := ix.QueryFingerprint(0, FingerprintBits, 0)
	if err != nil {
		t.Fatalf("QueryFingerprint on empty index: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("empty index: got %+v, want no matches", matches)
	}
}
