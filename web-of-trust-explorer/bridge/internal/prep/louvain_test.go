package prep

import "testing"

// Output length == nodeCount, and every node gets a community ID in [0, nodeCount).
func TestLouvain_OutputShape(t *testing.T) {
	edges := []uint32{0, 1, 1, 2, 2, 0, 3, 4, 4, 3}
	comm := Louvain(edges, 5)
	if len(comm) != 5 {
		t.Fatalf("len(comm) = %d, want 5", len(comm))
	}
	for i, c := range comm {
		if c >= 5 {
			t.Errorf("comm[%d] = %d out of range [0,5)", i, c)
		}
	}
}

// Two cliques joined by a single edge yield exactly 2 distinct community IDs
// for the clique members. Clique A = {0,1,2}, clique B = {3,4,5}, bridge 2-3.
func TestLouvain_TwoCliquesTwoCommunities(t *testing.T) {
	edges := []uint32{
		// clique A (undirected expressed as both directions)
		0, 1, 1, 0, 1, 2, 2, 1, 0, 2, 2, 0,
		// clique B
		3, 4, 4, 3, 4, 5, 5, 4, 3, 5, 5, 3,
		// single bridge edge between the cliques
		2, 3, 3, 2,
	}
	comm := Louvain(edges, 6)

	cliqueA := map[uint32]struct{}{comm[0]: {}, comm[1]: {}, comm[2]: {}}
	cliqueB := map[uint32]struct{}{comm[3]: {}, comm[4]: {}, comm[5]: {}}

	if len(cliqueA) != 1 {
		t.Errorf("clique A members span %d communities, want 1 (ids %d,%d,%d)", len(cliqueA), comm[0], comm[1], comm[2])
	}
	if len(cliqueB) != 1 {
		t.Errorf("clique B members span %d communities, want 1 (ids %d,%d,%d)", len(cliqueB), comm[3], comm[4], comm[5])
	}
	if comm[0] == comm[3] {
		t.Errorf("the two cliques collapsed into one community (both %d); want 2 distinct", comm[0])
	}

	distinct := map[uint32]struct{}{}
	for _, c := range comm {
		distinct[c] = struct{}{}
	}
	if len(distinct) != 2 {
		t.Errorf("got %d distinct communities, want exactly 2", len(distinct))
	}
}

// Empty edge list over nodeCount N returns N communities (each node its own),
// without panicking.
func TestLouvain_EmptyGraphSingletons(t *testing.T) {
	comm := Louvain(nil, 4)
	if len(comm) != 4 {
		t.Fatalf("len(comm) = %d, want 4", len(comm))
	}
	distinct := map[uint32]struct{}{}
	for _, c := range comm {
		distinct[c] = struct{}{}
	}
	if len(distinct) != 4 {
		t.Errorf("empty graph gave %d communities, want 4 singletons", len(distinct))
	}
}
