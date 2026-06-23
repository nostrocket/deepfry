package wire

import (
	"bytes"
	"encoding/binary"
	"testing"

	"wot-explorer-bridge/internal/dgraph"
)

func sampleGraph() *dgraph.GraphData {
	pk := make([]byte, 3*32)
	for i := 0; i < 3*32; i++ {
		pk[i] = byte(i)
	}
	return &dgraph.GraphData{
		Edges:          []uint32{0, 1, 0, 2, 1, 2},
		InDeg:          []uint32{0, 1, 2},
		OutDeg:         []uint32{2, 1, 0},
		Community:      []uint32{0, 0, 1},
		Kind3CreatedAt: []int32{1000, 2000, 3000},
		LastDbUpdate:   []int32{1100, 2100, 3100},
		Pubkeys:        pk,
		NodeCount:      3,
		EdgeCount:      3,
	}
}

// decodeFrame mirrors the browser-side decode: read header, then slice each
// section at its byte offset, asserting 4-byte alignment for every u32 section.
type decoded struct {
	magic, version, nodeCount, edgeCount uint32
	edges, inDeg, outDeg, community       []uint32
	kind3, lastU                          []int32
	pubkeys                               []byte
}

func decodeFrame(t *testing.T, buf []byte) decoded {
	t.Helper()
	le := binary.LittleEndian
	var d decoded
	d.magic = le.Uint32(buf[0:4])
	d.version = le.Uint32(buf[4:8])
	d.nodeCount = le.Uint32(buf[8:12])
	d.edgeCount = le.Uint32(buf[12:16])
	off := HeaderBytes
	if off%4 != 0 {
		t.Fatalf("header size %d not 4-byte aligned", off)
	}

	readU32 := func(name string, count int) []uint32 {
		if off%4 != 0 {
			t.Fatalf("%s section offset %d not 4-byte aligned", name, off)
		}
		out := make([]uint32, count)
		for i := 0; i < count; i++ {
			out[i] = le.Uint32(buf[off : off+4])
			off += 4
		}
		return out
	}
	readI32 := func(name string, count int) []int32 {
		if off%4 != 0 {
			t.Fatalf("%s section offset %d not 4-byte aligned", name, off)
		}
		out := make([]int32, count)
		for i := 0; i < count; i++ {
			out[i] = int32(le.Uint32(buf[off : off+4]))
			off += 4
		}
		return out
	}

	n := int(d.nodeCount)
	d.edges = readU32("edges", int(d.edgeCount)*2)
	d.inDeg = readU32("inDeg", n)
	d.outDeg = readU32("outDeg", n)
	d.community = readU32("community", n)
	d.kind3 = readI32("kind3", n)
	d.lastU = readI32("lastU", n)
	d.pubkeys = buf[off : off+n*32]
	off += n * 32
	return d
}

func TestEncode_RoundTrip(t *testing.T) {
	g := sampleGraph()
	var buf bytes.Buffer
	if err := Encode(&buf, g); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	d := decodeFrame(t, buf.Bytes())

	if d.magic != MagicWOTB {
		t.Errorf("magic = %#x, want %#x", d.magic, MagicWOTB)
	}
	if d.version != Version {
		t.Errorf("version = %d, want %d", d.version, Version)
	}
	if d.nodeCount != g.NodeCount || d.edgeCount != g.EdgeCount {
		t.Errorf("counts = %d,%d want %d,%d", d.nodeCount, d.edgeCount, g.NodeCount, g.EdgeCount)
	}

	assertU32Eq(t, "edges", d.edges, g.Edges)
	assertU32Eq(t, "inDeg", d.inDeg, g.InDeg)
	assertU32Eq(t, "outDeg", d.outDeg, g.OutDeg)
	assertU32Eq(t, "community", d.community, g.Community)
	assertI32Eq(t, "kind3", d.kind3, g.Kind3CreatedAt)
	assertI32Eq(t, "lastU", d.lastU, g.LastDbUpdate)

	if !bytes.Equal(d.pubkeys, g.Pubkeys) {
		t.Errorf("pubkey table did not round-trip byte-for-byte")
	}
	if len(d.pubkeys) != int(g.NodeCount)*32 {
		t.Errorf("pubkey table len = %d, want %d", len(d.pubkeys), int(g.NodeCount)*32)
	}
}

func assertU32Eq(t *testing.T, name string, got, want []uint32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len = %d, want %d", name, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %d, want %d", name, i, got[i], want[i])
		}
	}
}

func assertI32Eq(t *testing.T, name string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s len = %d, want %d", name, len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s[%d] = %d, want %d", name, i, got[i], want[i])
		}
	}
}
