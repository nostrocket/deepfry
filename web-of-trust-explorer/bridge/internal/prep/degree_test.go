package prep

import "testing"

// edges [0,1, 0,2, 1,2] over nodeCount 3 → outDeg [2,1,0], inDeg [0,1,2]
func TestDegrees_Basic(t *testing.T) {
	edges := []uint32{0, 1, 0, 2, 1, 2}
	inDeg, outDeg := Degrees(edges, 3)

	wantOut := []uint32{2, 1, 0}
	wantIn := []uint32{0, 1, 2}
	for i := range wantOut {
		if outDeg[i] != wantOut[i] {
			t.Errorf("outDeg[%d] = %d, want %d", i, outDeg[i], wantOut[i])
		}
		if inDeg[i] != wantIn[i] {
			t.Errorf("inDeg[%d] = %d, want %d", i, inDeg[i], wantIn[i])
		}
	}
}

// sum(outDeg) == sum(inDeg) == edgeCount for any input.
func TestDegrees_SumsEqualEdgeCount(t *testing.T) {
	edges := []uint32{0, 1, 1, 2, 2, 3, 3, 0, 0, 2}
	edgeCount := uint32(len(edges) / 2)
	inDeg, outDeg := Degrees(edges, 4)

	var sumIn, sumOut uint32
	for _, d := range inDeg {
		sumIn += d
	}
	for _, d := range outDeg {
		sumOut += d
	}
	if sumOut != edgeCount {
		t.Errorf("sum(outDeg) = %d, want edgeCount %d", sumOut, edgeCount)
	}
	if sumIn != edgeCount {
		t.Errorf("sum(inDeg) = %d, want edgeCount %d", sumIn, edgeCount)
	}
}

// In-degree over the follows edge list equals follower count (derived, no
// ~follows query). Node 0 is followed by 1,2,3 → inDeg[0] == 3 == follower count.
func TestDegrees_InDegreeIsFollowerCount(t *testing.T) {
	// 1→0, 2→0, 3→0 : node 0 has three followers.
	edges := []uint32{1, 0, 2, 0, 3, 0}
	inDeg, _ := Degrees(edges, 4)
	if inDeg[0] != 3 {
		t.Errorf("inDeg[0] (follower count) = %d, want 3", inDeg[0])
	}
}

// Empty edge list over nodeCount N → all-zero degrees, no panic, length N.
func TestDegrees_Empty(t *testing.T) {
	inDeg, outDeg := Degrees(nil, 5)
	if len(inDeg) != 5 || len(outDeg) != 5 {
		t.Fatalf("lengths = %d,%d, want 5,5", len(inDeg), len(outDeg))
	}
	for i := 0; i < 5; i++ {
		if inDeg[i] != 0 || outDeg[i] != 0 {
			t.Errorf("node %d degrees = %d,%d, want 0,0", i, inDeg[i], outDeg[i])
		}
	}
}
