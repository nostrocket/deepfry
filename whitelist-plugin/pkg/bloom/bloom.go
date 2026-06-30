// Package bloom provides a Builder (server-side filter construction) and an immutable
// Filter (plugin-side membership query and serialization) wrapping
// github.com/bits-and-blooms/bloom/v3.
//
// Wire format (DFBF — big-endian throughout, D-05/D-06/D-07):
//
//	magic[4]="DFBF" | formatVersion:uint8 | fpRate:float64 | gen[32] | payloadLen:uint64 | payload
//
// The payload is the library's WriteTo/MarshalBinary output verbatim; m and k ride
// inside it and are not re-stored in the header (D-06).
//
// HARD INVARIANT (D-02): this package uses strictly big-endian byte order throughout.
// The bitset global byte-order switch must never be used here — it silently corrupts
// the portable format (see D-02 in 01-CONTEXT.md).
package bloom

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	bbloom "github.com/bits-and-blooms/bloom/v3"
)

// Wire-format constants.
const (
	magic         = "DFBF"
	formatVersion = uint8(1)

	// maxPayloadBytes caps the declared payloadLen ReadFilter will accept before it
	// reads or allocates any payload bytes (D-07). It defeats two attacks from a
	// malicious or corrupt DFBF stream: a payloadLen above the platform's max int,
	// which would panic make([]byte, payloadLen) ("makeslice: len out of range"), and
	// an absurdly large value that would pre-commit memory and OOM the consuming
	// process. 1 GiB is generous headroom over any realistic pubkey filter (a 1e-6
	// filter for ~400M keys is ~1 GiB) while keeping the failure mode a clean
	// ErrBadFormat rather than a crash.
	maxPayloadBytes = uint64(1) << 30 // 1 GiB
)

// Error sentinels returned by ReadFilter and related operations.
var (
	// ErrBadFormat is returned when the magic bytes or format version do not match.
	ErrBadFormat = errors.New("bloom: bad format")
	// ErrTruncated is returned when the byte stream ends before the declared payload length.
	ErrTruncated = errors.New("bloom: truncated")
	// ErrUnsupportedVersion is returned when the format version byte is not recognized.
	ErrUnsupportedVersion = errors.New("bloom: unsupported version")
)

// Builder constructs a bloom filter server-side. It is mutable; call Build to freeze it
// into an immutable Filter.
type Builder struct {
	bf *bbloom.BloomFilter
	fp float64
}

// NewBuilder returns a Builder sized to hold n elements at false-positive rate fp using
// the library's NewWithEstimates (D-01). Sizing knobs beyond (n, fp) are not exposed.
func NewBuilder(n uint, fp float64) *Builder {
	return &Builder{
		bf: bbloom.NewWithEstimates(n, fp),
		fp: fp,
	}
}

// Add inserts k into the filter. It passes k[:] to the underlying library — an
// alloc-free stack slice header; no heap copy is made (D-08).
func (b *Builder) Add(k [32]byte) {
	b.bf.Add(k[:])
}

// AddHex decodes a 64-character lowercase hex string and calls Add. It is the strict
// boundary helper (analog: repository.go hexTo32ByteArray): bad length or non-hex input
// returns a wrapped error over ErrBadFormat rather than silently treating the key as
// not-present. Hex decode lives only here, never on the hot path (D-10).
func (b *Builder) AddHex(s string) error {
	s = strings.ToLower(s)
	if len(s) != 64 {
		return fmt.Errorf("bloom: AddHex: expected 64 hex chars, got %d: %w", len(s), ErrBadFormat)
	}
	var k [32]byte
	if _, err := hex.Decode(k[:], []byte(s)); err != nil {
		return fmt.Errorf("bloom: AddHex: invalid hex: %w", err)
	}
	b.Add(k)
	return nil
}

// Build freezes the filter and returns an immutable *Filter. The generation marker is
// sha256.Sum256(bf.MarshalBinary()) as specified in D-03. After Build the Builder must
// not be used (behavior is undefined). The returned *Filter is safe to store behind an
// atomic.Pointer[Filter] and read concurrently (D-09 immutability contract).
func (b *Builder) Build() (*Filter, error) {
	payload, err := b.bf.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("bloom: Build: MarshalBinary: %w", err)
	}
	gen := sha256.Sum256(payload)
	return &Filter{
		bf:      b.bf,
		fp:      b.fp,
		gen:     gen,
		payload: payload,
	}, nil
}

// Filter is an immutable, concurrency-safe bloom filter. It is safe to read via multiple
// goroutines after being returned from Builder.Build (D-09).
type Filter struct {
	bf      *bbloom.BloomFilter
	fp      float64
	gen     [32]byte
	payload []byte // cached MarshalBinary output — avoids recomputation on WriteTo
}

// Contains reports whether k is possibly present in the filter. It passes k[:] — an
// alloc-free slice view — to the underlying Test call. This is the 0-allocs hot path
// (D-08). Do NOT call hex decode here.
func (f *Filter) Contains(k [32]byte) bool {
	return f.bf.Test(k[:])
}

// ContainsHex decodes a 64-character hex string and calls Contains. This is the lenient
// boundary helper (analog: whitelist.go IsWhitelisted): a bad length or non-hex string
// is treated as not-present and returns (false, nil) without panicking (D-10 query-side
// contract). Hex decode is isolated here and never runs per-event on the hot path.
func (f *Filter) ContainsHex(s string) (bool, error) {
	if len(s) != 64 {
		return false, nil
	}
	var k [32]byte
	if _, err := hex.Decode(k[:], []byte(strings.ToLower(s))); err != nil {
		return false, nil
	}
	return f.Contains(k), nil
}

// Generation returns the 32-byte content-hash marker computed at Build time as
// sha256.Sum256(MarshalBinary()). The same marker is exposed as ETag (D-04).
func (f *Filter) Generation() [32]byte {
	return f.gen
}

// ETag returns the generation marker as a quoted lowercase hex string suitable for use
// as an HTTP ETag in Phase 2 (D-04). No recomputation — derived from the stored marker.
func (f *Filter) ETag() string {
	return `"` + hex.EncodeToString(f.gen[:]) + `"`
}

// FalsePositiveRate returns the build-time fp parameter (proves success criterion 4:
// fp is a parameter not a constant).
func (f *Filter) FalsePositiveRate() float64 {
	return f.fp
}

// WriteTo serializes the filter to w in the custom DFBF big-endian format (D-05).
// Layout: magic[4] | formatVersion:uint8 | fpRate:float64 | gen[32] | payloadLen:uint64 | payload.
// fpRate is encoded via math.Float64bits (D-06). Returns total bytes written.
func (f *Filter) WriteTo(w io.Writer) (int64, error) {
	var n int64

	// magic[4]
	nn, err := io.WriteString(w, magic)
	n += int64(nn)
	if err != nil {
		return n, fmt.Errorf("bloom: WriteTo: write magic: %w", err)
	}

	// formatVersion:uint8
	if err = binary.Write(w, binary.BigEndian, formatVersion); err != nil {
		return n, fmt.Errorf("bloom: WriteTo: write version: %w", err)
	}
	n++

	// fpRate:float64 via Float64bits (D-06)
	if err = binary.Write(w, binary.BigEndian, math.Float64bits(f.fp)); err != nil {
		return n, fmt.Errorf("bloom: WriteTo: write fpRate: %w", err)
	}
	n += 8

	// gen[32]
	nn, err = w.Write(f.gen[:])
	n += int64(nn)
	if err != nil {
		return n, fmt.Errorf("bloom: WriteTo: write gen: %w", err)
	}

	// payloadLen:uint64
	if err = binary.Write(w, binary.BigEndian, uint64(len(f.payload))); err != nil {
		return n, fmt.Errorf("bloom: WriteTo: write payloadLen: %w", err)
	}
	n += 8

	// payload verbatim
	nn, err = w.Write(f.payload)
	n += int64(nn)
	if err != nil {
		return n, fmt.Errorf("bloom: WriteTo: write payload: %w", err)
	}

	return n, nil
}

// MarshalBinary serializes the filter to a byte slice via WriteTo over a bytes.Buffer.
func (f *Filter) MarshalBinary() ([]byte, error) {
	var buf bytes.Buffer
	if _, err := f.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ReadFilter deserializes a Filter from r in the DFBF format. It validates:
//   - 4-byte magic (mismatch → ErrBadFormat)
//   - formatVersion (unknown → ErrUnsupportedVersion, wrapped with ErrBadFormat)
//   - payloadLen bound-check before consuming bytes (short read → ErrTruncated, D-07)
//
// All IO and decode failures are wrapped with fmt.Errorf so callers can use errors.Is.
func ReadFilter(r io.Reader) (*Filter, error) {
	// magic[4]
	var magicBuf [4]byte
	if _, err := io.ReadFull(r, magicBuf[:]); err != nil {
		return nil, fmt.Errorf("bloom: ReadFilter: read magic: %w", ErrTruncated)
	}
	if string(magicBuf[:]) != magic {
		return nil, fmt.Errorf("bloom: ReadFilter: wrong magic %q: %w", magicBuf, ErrBadFormat)
	}

	// formatVersion:uint8
	var ver uint8
	if err := binary.Read(r, binary.BigEndian, &ver); err != nil {
		return nil, fmt.Errorf("bloom: ReadFilter: read version: %w", ErrTruncated)
	}
	if ver != formatVersion {
		return nil, fmt.Errorf("bloom: ReadFilter: unsupported format version %d: %w", ver, ErrUnsupportedVersion)
	}

	// fpRate:float64
	var fpBits uint64
	if err := binary.Read(r, binary.BigEndian, &fpBits); err != nil {
		return nil, fmt.Errorf("bloom: ReadFilter: read fpRate: %w", ErrTruncated)
	}
	fp := math.Float64frombits(fpBits)
	// Validate the deserialized fp-rate (WR-03). The generation marker covers only the
	// payload, not the header, so a crafted header can inject NaN/Inf/negative/>=1 that
	// would otherwise ride past this boundary into FalsePositiveRate() undetected.
	if math.IsNaN(fp) || math.IsInf(fp, 0) || fp <= 0 || fp >= 1 {
		return nil, fmt.Errorf("bloom: ReadFilter: invalid fpRate %v: %w", fp, ErrBadFormat)
	}

	// gen[32]
	var gen [32]byte
	if _, err := io.ReadFull(r, gen[:]); err != nil {
		return nil, fmt.Errorf("bloom: ReadFilter: read gen: %w", ErrTruncated)
	}

	// payloadLen:uint64
	var payloadLen uint64
	if err := binary.Read(r, binary.BigEndian, &payloadLen); err != nil {
		return nil, fmt.Errorf("bloom: ReadFilter: read payloadLen: %w", ErrTruncated)
	}

	// Bound-check the declared length BEFORE allocating or reading any payload bytes
	// (D-07). Without this, make([]byte, payloadLen) panics when payloadLen exceeds the
	// platform max int, and an absurd value pre-commits memory — a remote DoS for any
	// consumer of an untrusted /bloom response.
	if payloadLen > maxPayloadBytes {
		return nil, fmt.Errorf("bloom: ReadFilter: payloadLen %d exceeds max %d: %w", payloadLen, maxPayloadBytes, ErrBadFormat)
	}

	// Read exactly payloadLen bytes via io.CopyN, which grows the buffer incrementally
	// as bytes arrive rather than pre-committing the declared size up front. A stream
	// that declares payloadLen but delivers fewer yields a short copy → ErrTruncated,
	// without ever allocating the full declared length.
	var payloadBuf bytes.Buffer
	if _, err := io.CopyN(&payloadBuf, r, int64(payloadLen)); err != nil {
		return nil, fmt.Errorf("bloom: ReadFilter: read payload (declared %d bytes): %w", payloadLen, ErrTruncated)
	}
	payload := payloadBuf.Bytes()

	// Reconstruct underlying filter from payload.
	bf := new(bbloom.BloomFilter)
	if err := bf.UnmarshalBinary(payload); err != nil {
		return nil, fmt.Errorf("bloom: ReadFilter: UnmarshalBinary: %w", err)
	}

	return &Filter{
		bf:      bf,
		fp:      fp,
		gen:     gen,
		payload: payload,
	}, nil
}
