// Package prep is the server-side data-prep pass over the dense uint32 edge
// list: in/out-degree (O(E)) and array-based Louvain community detection. It
// operates on flat []uint32 SoA slices only — never per-node heap objects — so
// it scales to the 1.5M-node graph without the memory blowup this phase exists
// to eliminate (RESEARCH Pitfall 1).
package prep

// Degrees computes in- and out-degree in a single O(E) pass over the dense edge
// list. For each [src,tgt] pair: outDeg[src]++ (follows) and inDeg[tgt]++
// (followers). In-degree is DERIVED from the follows edge list — it is NOT
// queried via ~follows — so in-degree over the follows edges equals follower
// count (D-04; matches the Phase-1 discipline and the wote-followers-data-model
// memory note). Both returned slices have length nodeCount.
func Degrees(edges []uint32, nodeCount uint32) (inDeg, outDeg []uint32) {
	inDeg = make([]uint32, nodeCount)
	outDeg = make([]uint32, nodeCount)
	for i := 0; i+1 < len(edges); i += 2 {
		src, tgt := edges[i], edges[i+1]
		if src < nodeCount {
			outDeg[src]++
		}
		if tgt < nodeCount {
			inDeg[tgt]++
		}
	}
	return inDeg, outDeg
}
