package prep

// Louvain assigns a community ID per node by modularity maximization over the
// dense uint32 edge list. It is a HAND-ROLLED ARRAY implementation: communities,
// weighted degrees, and per-community totals are held in flat slices and merged
// in place. We deliberately do NOT route the graph through gonum's interface
// graph.Graph model — that allocates a heap object per node/edge and reintroduces
// the exact memory blowup this phase exists to kill, and has a documented
// infinite-loop bug on small ΔQ (RESEARCH Pitfall 1, gonum#1488). gonum's
// directed-modularity Q is referenced only as a correctness guide.
//
// Edges are directed (follows), but for COLORING we maximize UNDIRECTED
// modularity over the symmetrized graph: each directed edge contributes weight 1
// to the undirected edge weight between its endpoints. This is cheaper than
// directed modularity and produces the cluster coloring OVER-02 needs.
//
// FALLBACK LADDER (Assumption A2 / Open Question 1 — full-scale array-Louvain
// wall-clock at 1.5M nodes / tens-of-M edges is UNVERIFIED and is a one-time
// post-load cost; the Plan 03 verdict records the measured server-compute ms
// that decides this):
//
//	(a) THIS is already the cheaper undirected pass.
//	(b) If even undirected Louvain proves too slow in the Plan 03 verdict run,
//	    defer community to a follow-up and return all-zeros from this function —
//	    degree alone still unblocks OVER-01 (node sizing/coloring by degree).
//
// Termination is bounded two ways so the pass always halts: a per-level
// local-moving sweep stops when the modularity gain in a full pass drops below
// epsilon, and the outer level loop stops when a level makes no moves or after
// maxLevels.
func Louvain(edges []uint32, nodeCount uint32) []uint32 {
	const (
		epsilon   = 1e-7
		maxLevels = 20
		maxSweeps = 100
	)

	n := int(nodeCount)
	result := make([]uint32, n)
	for i := range result {
		result[i] = uint32(i) // start: every node its own community (singletons)
	}
	if n == 0 || len(edges) < 2 {
		return result // empty graph → N singletons, no panic
	}

	// Build the symmetrized weighted adjacency for the current level as
	// adjacency lists over flat slices. Initially one super-node per input node.
	g := buildLevelGraph(edges, n)

	for level := 0; level < maxLevels; level++ {
		comm, moved := g.localMoving(epsilon, maxSweeps)
		if !moved {
			break
		}
		// Propagate this level's community assignment back to the original
		// node indices via the running result mapping.
		relabelResult(result, g.nodeOf, comm)
		// Aggregate communities into super-nodes for the next level.
		next, singleCommunity := g.aggregate(comm)
		if singleCommunity {
			break
		}
		g = next
	}

	return canonicalize(result)
}

// levelGraph is a weighted undirected graph for one Louvain level, stored as
// flat adjacency lists. nodeOf maps each super-node back to the list of original
// node indices it represents.
type levelGraph struct {
	size      int         // number of super-nodes at this level
	neighbors [][]int     // adjacency list per super-node
	weights   [][]float64 // edge weight parallel to neighbors
	selfLoop  []float64   // accumulated self-loop weight per super-node
	degree    []float64   // weighted degree per super-node (incl. self-loops*2)
	totalW    float64     // total edge weight (m); 2m used in modularity
	nodeOf    [][]int     // original node indices represented by each super-node
}

func buildLevelGraph(edges []uint32, n int) *levelGraph {
	// Symmetrize: accumulate undirected weight between endpoint pairs.
	type key struct{ a, b int }
	wsum := make(map[key]float64)
	for i := 0; i+1 < len(edges); i += 2 {
		s, t := int(edges[i]), int(edges[i+1])
		if s >= n || t >= n {
			continue
		}
		if s == t {
			wsum[key{s, s}] += 1 // self loop
			continue
		}
		a, b := s, t
		if a > b {
			a, b = b, a
		}
		wsum[key{a, b}] += 1
	}

	g := &levelGraph{
		size:      n,
		neighbors: make([][]int, n),
		weights:   make([][]float64, n),
		selfLoop:  make([]float64, n),
		degree:    make([]float64, n),
		nodeOf:    make([][]int, n),
	}
	for i := 0; i < n; i++ {
		g.nodeOf[i] = []int{i}
	}
	for k, w := range wsum {
		if k.a == k.b {
			g.selfLoop[k.a] += w
			g.degree[k.a] += 2 * w
			g.totalW += w
			continue
		}
		g.neighbors[k.a] = append(g.neighbors[k.a], k.b)
		g.weights[k.a] = append(g.weights[k.a], w)
		g.neighbors[k.b] = append(g.neighbors[k.b], k.a)
		g.weights[k.b] = append(g.weights[k.b], w)
		g.degree[k.a] += w
		g.degree[k.b] += w
		g.totalW += w
	}
	return g
}

// localMoving runs the Louvain local-moving phase: repeatedly move each node to
// the neighboring community that yields the best modularity gain, until a full
// sweep produces gain below epsilon (or maxSweeps). Returns the community label
// per super-node and whether any node moved away from its singleton start.
func (g *levelGraph) localMoving(epsilon float64, maxSweeps int) (comm []int, moved bool) {
	twoM := 2 * g.totalW
	if twoM == 0 {
		// No edges at this level: every super-node its own community.
		comm = make([]int, g.size)
		for i := range comm {
			comm[i] = i
		}
		return comm, false
	}

	comm = make([]int, g.size)
	// sigmaTot[c] = sum of weighted degrees of nodes in community c.
	sigmaTot := make([]float64, g.size)
	for i := 0; i < g.size; i++ {
		comm[i] = i
		sigmaTot[i] = g.degree[i]
	}

	for sweep := 0; sweep < maxSweeps; sweep++ {
		var gain float64
		sweepMoved := false
		for v := 0; v < g.size; v++ {
			cur := comm[v]
			// Remove v from its community.
			sigmaTot[cur] -= g.degree[v]
			comm[v] = -1

			// Sum edge weight from v into each candidate community.
			kIn := map[int]float64{}
			for j, u := range g.neighbors[v] {
				kIn[comm[u]] += g.weights[v][j]
			}

			bestC := cur
			bestGain := 0.0
			// ΔQ of moving v into community c (undirected):
			//   kIn[c]/twoM - sigmaTot[c]*degree[v]/(twoM^2)
			// We compare against staying (cur), so the baseline is cur's ΔQ.
			baseGain := kIn[cur]/g.totalW - sigmaTot[cur]*g.degree[v]/(twoM*g.totalW)
			for c, ki := range kIn {
				dq := ki/g.totalW - sigmaTot[c]*g.degree[v]/(twoM*g.totalW)
				if dq > bestGain+1e-12 && dq > baseGain {
					bestGain = dq
					bestC = c
				}
			}

			comm[v] = bestC
			sigmaTot[bestC] += g.degree[v]
			if bestC != cur {
				sweepMoved = true
				moved = true
				gain += bestGain - baseGain
			}
		}
		if !sweepMoved || gain < epsilon {
			break
		}
	}
	return comm, moved
}

// aggregate collapses each community into a super-node for the next level.
// Returns the next-level graph and whether everything collapsed into a single
// community (a terminal condition).
func (g *levelGraph) aggregate(comm []int) (*levelGraph, bool) {
	// Renumber communities to a dense 0..k-1 range.
	remap := map[int]int{}
	for _, c := range comm {
		if _, ok := remap[c]; !ok {
			remap[c] = len(remap)
		}
	}
	k := len(remap)
	if k <= 1 {
		return nil, true
	}

	next := &levelGraph{
		size:      k,
		neighbors: make([][]int, k),
		weights:   make([][]float64, k),
		selfLoop:  make([]float64, k),
		degree:    make([]float64, k),
		nodeOf:    make([][]int, k),
	}
	// nodeOf: union of original nodes per new super-node.
	for v, c := range comm {
		nc := remap[c]
		next.nodeOf[nc] = append(next.nodeOf[nc], g.nodeOf[v]...)
	}

	// Accumulate inter- and intra-community weights.
	type key struct{ a, b int }
	wsum := map[key]float64{}
	for v := 0; v < g.size; v++ {
		cv := remap[comm[v]]
		// self-loop weight carries forward into the super-node self-loop.
		next.selfLoop[cv] += g.selfLoop[v]
		next.degree[cv] += 2 * g.selfLoop[v]
		next.totalW += g.selfLoop[v]
		for j, u := range g.neighbors[v] {
			cu := remap[comm[u]]
			w := g.weights[v][j]
			if cv == cu {
				// intra-community edge; counted once (v<u guard via half).
				if v < u {
					next.selfLoop[cv] += w
					next.degree[cv] += 2 * w
					next.totalW += w
				}
				continue
			}
			if v < u {
				a, b := cv, cu
				if a > b {
					a, b = b, a
				}
				wsum[key{a, b}] += w
			}
		}
	}
	for kk, w := range wsum {
		next.neighbors[kk.a] = append(next.neighbors[kk.a], kk.b)
		next.weights[kk.a] = append(next.weights[kk.a], w)
		next.neighbors[kk.b] = append(next.neighbors[kk.b], kk.a)
		next.weights[kk.b] = append(next.weights[kk.b], w)
		next.degree[kk.a] += w
		next.degree[kk.b] += w
		next.totalW += w
	}
	return next, false
}

// relabelResult pushes a level's community labels back down to the original node
// indices. nodeOf[s] lists the original nodes inside super-node s; comm[s] is
// that super-node's community at this level.
func relabelResult(result []uint32, nodeOf [][]int, comm []int) {
	for s, orig := range nodeOf {
		c := uint32(comm[s])
		for _, o := range orig {
			result[o] = c
		}
	}
}

// canonicalize renumbers community IDs to a dense 0..k-1 range in first-seen
// order so every node carries an ID in [0, nodeCount).
func canonicalize(result []uint32) []uint32 {
	remap := map[uint32]uint32{}
	for i, c := range result {
		nc, ok := remap[c]
		if !ok {
			nc = uint32(len(remap))
			remap[c] = nc
		}
		result[i] = nc
	}
	return result
}
