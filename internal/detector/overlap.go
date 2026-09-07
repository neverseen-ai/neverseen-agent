package detector

import (
	"sort"

	"github.com/neverseen-ai/neverseen-agent/pkg/pii"
)

// resolveOverlaps keeps one match per stretch of text and returns the survivors
// in reading order.
//
// Arbitration, in order: a credential always wins, then confidence, then the
// longer span, then the leftmost. Position deciding on its own is the trap —
// with "whichever starts later wins", the postal code inside "12 rue de la
// Paix, 75002 Paris" evicted the address containing it and the street went to
// the provider in clear. A weaker pattern must never take a span from a
// stronger one just by starting further right.
//
// Credentials outrank the confidence scale rather than sitting on it. Several
// ordinary categories score above a connection string, so
// "postgres://admin:pw@db" resolved to an email match over the password — and a
// credential that reaches the vault under some other category's token is a
// credential the response path will expand back into a live secret.
func resolveOverlaps(matches []Match) []Match {
	if len(matches) <= 1 {
		return matches
	}

	// Cluster first: only matches that overlap transitively compete, and a body
	// is overwhelmingly made of matches that touch nothing else. Ranking each
	// cluster on its own keeps this O(n log n + Σk²) with k the cluster size,
	// rather than comparing every match against every survivor.
	//
	// TODO: Σk² is the remaining ceiling. It is bounded in practice — patterns
	// rarely stack more than a handful of candidates on one span — but a crafted
	// body could nest many. Cap the cluster size, or move the greedy pick to an
	// interval tree, if that ever shows up in a profile.
	byStart := make([]Match, len(matches))
	copy(byStart, matches)
	sort.SliceStable(byStart, func(i, j int) bool { return byStart[i].Start < byStart[j].Start })

	var kept []Match
	for start := 0; start < len(byStart); {
		end, reach := start+1, byStart[start].End
		for end < len(byStart) && byStart[end].Start < reach {
			if byStart[end].End > reach {
				reach = byStart[end].End
			}
			end++
		}
		kept = append(kept, pickFromCluster(byStart[start:end])...)
		start = end
	}

	sort.SliceStable(kept, func(i, j int) bool { return kept[i].Start < kept[j].Start })
	return kept
}

// pickFromCluster greedily keeps the strongest non-overlapping matches of one
// transitively-overlapping group.
//
// TODO: a losing match is dropped whole — the stretch it covered beyond the
// winner is not reclaimed, so a longer weak match partially overlapping a
// shorter strong one loses its tail to the provider in clear. The patterns rely
// on their spans agreeing instead, which the address and postal-code pair do by
// sharing the capital-anchored commune rule. Generalise to clipping if the
// corpus ever surfaces another diverging pair.
func pickFromCluster(cluster []Match) []Match {
	if len(cluster) == 1 {
		return cluster
	}

	ranked := make([]Match, len(cluster))
	copy(ranked, cluster)
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if sa, sb := pii.IsSecret(a.Category), pii.IsSecret(b.Category); sa != sb {
			return sa
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if la, lb := a.End-a.Start, b.End-b.Start; la != lb {
			return la > lb // the longer span covers more of the value
		}
		return a.Start < b.Start
	})

	var kept []Match
	for _, m := range ranked {
		if !overlapsAny(m, kept) {
			kept = append(kept, m)
		}
	}
	return kept
}

func overlapsAny(m Match, others []Match) bool {
	for _, o := range others {
		if m.Start < o.End && o.Start < m.End {
			return true
		}
	}
	return false
}
