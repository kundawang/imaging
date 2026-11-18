package imaging

import (
	"errors"
	"image"
	"image/color"
	"math/bits"
	"sort"
	"sync"
)

// FingerprintSize is the edge length of the grayscale grid an image is
// reduced to before hashing.
const FingerprintSize = 8

// FingerprintBits is the fixed number of bits in a Fingerprint.
const FingerprintBits = FingerprintSize * FingerprintSize

// Fingerprint is a fixed-length perceptual fingerprint of an image.
// It is deterministic and insensitive to image size and light compression
// noise. The perceptual distance between two images is the Hamming
// distance between their fingerprints (see Distance).
type Fingerprint uint64

// Errors returned by the perceptual hashing functions and HashIndex.
var (
	// ErrEmptyImage is returned when the image is nil or has empty bounds.
	ErrEmptyImage = errors.New("imaging: cannot hash an empty image")
	// ErrDuplicateID is returned when adding an ID that already exists in the index.
	ErrDuplicateID = errors.New("imaging: duplicate asset ID")
	// ErrIndexFull is returned when the index reached its entry capacity.
	ErrIndexFull = errors.New("imaging: hash index is full")
	// ErrInvalidDistance is returned when a distance threshold is out of range.
	ErrInvalidDistance = errors.New("imaging: distance threshold out of range [0, 64]")
	// ErrInvalidCapacity is returned when the index capacity is not positive.
	ErrInvalidCapacity = errors.New("imaging: index capacity must be positive")
)

// HashImage computes the perceptual fingerprint of img. Pixels are
// composited over the given background color first, so transparent and
// semi-transparent images are hashed the way they would appear on that
// background. The image is then downscaled to a fixed 8x8 grayscale grid
// and each of the 64 bits is set if the corresponding cell is brighter
// than the grid mean. A fully constant image therefore hashes to 0.
//
// Returns ErrEmptyImage if img is nil or has zero-area bounds.
func HashImage(img image.Image, background color.Color) (Fingerprint, error) {
	if img == nil {
		return 0, ErrEmptyImage
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return 0, ErrEmptyImage
	}

	flat := Paste(New(b.Dx(), b.Dy(), background), img, image.Point{})
	small := Resize(Grayscale(flat), FingerprintSize, FingerprintSize, Lanczos)

	var pix [FingerprintBits]uint8
	var sum uint32
	for y := 0; y < FingerprintSize; y++ {
		for x := 0; x < FingerprintSize; x++ {
			v := small.Pix[y*small.Stride+x*4]
			pix[y*FingerprintSize+x] = v
			sum += uint32(v)
		}
	}
	mean := sum / FingerprintBits

	var fp Fingerprint
	for i, v := range pix {
		if uint32(v) > mean {
			fp |= 1 << uint(i)
		}
	}
	return fp, nil
}

// Distance returns the Hamming distance between two fingerprints, in the
// range [0, FingerprintBits]. Lower values mean more similar images.
func Distance(a, b Fingerprint) int {
	return bits.OnesCount64(uint64(a ^ b))
}

// Match is a single query result: an indexed asset ID, its fingerprint
// and its distance to the query fingerprint.
type Match struct {
	ID          string
	Fingerprint Fingerprint
	Distance    int
}

type hashEntry struct {
	fp  Fingerprint
	seq uint64 // insertion sequence, used for stable ordering
}

// HashIndex is an in-memory index mapping asset IDs to perceptual
// fingerprints. Multiple IDs may share the same fingerprint. The index
// holds at most maxEntries entries, bounding its memory use. It is safe
// for concurrent use.
type HashIndex struct {
	mu         sync.RWMutex
	background color.Color
	maxEntries int
	entries    map[string]hashEntry
	seq        uint64
}

// NewHashIndex creates an empty index. background is used to composite
// transparent images at both Add and Query time, so the same value must
// be used consistently. maxEntries bounds the number of stored assets
// (and thus memory); it must be positive.
func NewHashIndex(background color.Color, maxEntries int) (*HashIndex, error) {
	if maxEntries <= 0 {
		return nil, ErrInvalidCapacity
	}
	return &HashIndex{
		background: background,
		maxEntries: maxEntries,
		entries:    make(map[string]hashEntry),
	}, nil
}

// Len returns the number of assets currently in the index.
func (ix *HashIndex) Len() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.entries)
}

// Add computes the fingerprint of img and stores it under id.
// It returns ErrDuplicateID if id is already indexed, ErrIndexFull if
// the index reached its capacity, or ErrEmptyImage for an empty image.
func (ix *HashIndex) Add(id string, img image.Image) error {
	fp, err := HashImage(img, ix.background)
	if err != nil {
		return err
	}
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, ok := ix.entries[id]; ok {
		return ErrDuplicateID
	}
	if len(ix.entries) >= ix.maxEntries {
		return ErrIndexFull
	}
	ix.entries[id] = hashEntry{fp: fp, seq: ix.seq}
	ix.seq++
	return nil
}

// Remove deletes id from the index. It reports whether the ID was present.
func (ix *HashIndex) Remove(id string) bool {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	if _, ok := ix.entries[id]; !ok {
		return false
	}
	delete(ix.entries, id)
	return true
}

// Query hashes img and returns the indexed assets within maxDistance of
// it. Results are sorted by distance ascending; ties are broken by
// insertion order, so the ordering is stable. limit caps the number of
// returned matches; limit <= 0 returns all matches within maxDistance.
// maxDistance must be in [0, FingerprintBits].
func (ix *HashIndex) Query(img image.Image, maxDistance, limit int) ([]Match, error) {
	fp, err := HashImage(img, ix.background)
	if err != nil {
		return nil, err
	}
	return ix.QueryFingerprint(fp, maxDistance, limit)
}

// QueryFingerprint returns the indexed assets within maxDistance of fp.
// It behaves like Query but takes a precomputed fingerprint.
func (ix *HashIndex) QueryFingerprint(fp Fingerprint, maxDistance, limit int) ([]Match, error) {
	if maxDistance < 0 || maxDistance > FingerprintBits {
		return nil, ErrInvalidDistance
	}
	ix.mu.RLock()
	matches := make([]Match, 0, len(ix.entries))
	for id, e := range ix.entries {
		d := Distance(fp, e.fp)
		if d <= maxDistance {
			matches = append(matches, Match{ID: id, Fingerprint: e.fp, Distance: d})
		}
	}
	// Sort by distance, then by insertion sequence for a stable,
	// deterministic order. The seq values are read under the same lock.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Distance != matches[j].Distance {
			return matches[i].Distance < matches[j].Distance
		}
		return ix.entries[matches[i].ID].seq < ix.entries[matches[j].ID].seq
	})
	ix.mu.RUnlock()
	if limit > 0 && len(matches) > limit {
		matches = matches[:limit]
	}
	return matches, nil
}
