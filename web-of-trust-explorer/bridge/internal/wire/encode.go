// Package wire encodes the GraphData into a self-describing little-endian
// binary frame the browser decodes WITHOUT JSON.parse — the load-bearing win of
// PERF-01. All multi-byte ints are LITTLE-ENDIAN (host order on the target
// x86/ARM machines), so a browser `new Uint32Array(buffer, offset, n)` view is
// zero-cost; big-endian would force a per-element byte-swap in JS.
//
// Frame layout (RESEARCH Pattern 3):
//
//	[ MAGIC u32 "WOTB" ][ VERSION u32 ][ nodeCount u32 ][ edgeCount u32 ]   # 16-byte header
//	[ edges:          u32 × edgeCount*2  ]   # src,tgt dense-index pairs
//	[ inDeg:          u32 × nodeCount    ]
//	[ outDeg:         u32 × nodeCount    ]
//	[ community:      u32 × nodeCount    ]
//	[ kind3CreatedAt: i32 × nodeCount    ]
//	[ lastDbUpdate:   i32 × nodeCount    ]
//	[ pubkeyBytes:    32 × nodeCount     ]   # packed 32-byte binary, LAST so all
//	                                         # u32 sections stay 4-byte aligned (Pitfall 4)
package wire

import (
	"encoding/binary"
	"fmt"
	"io"

	"wot-explorer-bridge/internal/dgraph"
)

// MagicWOTB is the frame magic, ASCII "WOTB" read little-endian. The browser
// rejects any body whose first u32 != this (truncated/incompatible frame guard,
// threat T-01.1-04).
const MagicWOTB uint32 = 0x42544F57 // 'W'=0x57,'O'=0x4F,'T'=0x54,'B'=0x42 little-endian

// Version is the frame format version; bump on any layout change so the browser
// can reject an incompatible body.
const Version uint32 = 1

// HeaderBytes is the fixed header size: MAGIC + VERSION + nodeCount + edgeCount.
const HeaderBytes = 16

// Section is one writable, independently-flushable region of the frame so the
// HTTP handler can Write+Flush each one for a visible byte counter (D-09).
type Section struct {
	Name  string
	Bytes []byte
}

// Sections returns the frame split into independently-flushable sections in
// wire order. The HTTP handler writes and flushes each in turn.
func Sections(g *dgraph.GraphData) []Section {
	le := binary.LittleEndian

	header := make([]byte, HeaderBytes)
	le.PutUint32(header[0:4], MagicWOTB)
	le.PutUint32(header[4:8], Version)
	le.PutUint32(header[8:12], g.NodeCount)
	le.PutUint32(header[12:16], g.EdgeCount)

	n := int(g.NodeCount)

	return []Section{
		{"header", header},
		{"edges", packU32(g.Edges)},
		{"inDeg", packU32(fitU32(g.InDeg, n))},
		{"outDeg", packU32(fitU32(g.OutDeg, n))},
		{"community", packU32(fitU32(g.Community, n))},
		{"kind3CreatedAt", packI32(fitI32(g.Kind3CreatedAt, n))},
		{"lastDbUpdate", packI32(fitI32(g.LastDbUpdate, n))},
		{"pubkeys", fitBytes(g.Pubkeys, n*32)},
	}
}

// Encode writes the whole frame to w in one pass (used by tests and any
// non-chunked caller). The HTTP handler uses Sections directly so it can flush.
func Encode(w io.Writer, g *dgraph.GraphData) error {
	for _, s := range Sections(g) {
		if _, err := w.Write(s.Bytes); err != nil {
			return fmt.Errorf("encode section %s: %w", s.Name, err)
		}
	}
	return nil
}

func packU32(v []uint32) []byte {
	out := make([]byte, len(v)*4)
	le := binary.LittleEndian
	for i, x := range v {
		le.PutUint32(out[i*4:i*4+4], x)
	}
	return out
}

func packI32(v []int32) []byte {
	out := make([]byte, len(v)*4)
	le := binary.LittleEndian
	for i, x := range v {
		le.PutUint32(out[i*4:i*4+4], uint32(x))
	}
	return out
}

// fitU32 returns a length-n slice: truncates or zero-pads to keep section sizes
// exactly nodeCount even if a prep pass returned a short/nil slice.
func fitU32(v []uint32, n int) []uint32 {
	if len(v) == n {
		return v
	}
	out := make([]uint32, n)
	copy(out, v)
	return out
}

func fitI32(v []int32, n int) []int32 {
	if len(v) == n {
		return v
	}
	out := make([]int32, n)
	copy(out, v)
	return out
}

func fitBytes(v []byte, n int) []byte {
	if len(v) == n {
		return v
	}
	out := make([]byte, n)
	copy(out, v)
	return out
}
