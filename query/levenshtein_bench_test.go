package query_test

import (
	"strings"
	"testing"

	"github.com/RogueTeam/textiplex/query"
)

func BenchmarkLevenshtein(b *testing.B) {
	var levenshteinCases = []struct {
		name   string
		s1, s2 string
		k      int
	}{
		// k=1 match: single deletion
		{"short/match/k1", "acount", "account", 1},
		// k=1 match: single substitution
		{"short/match_sub/k1", "accoult", "account", 1},
		// k=1 no match: distance is 2
		{"short/nomatch/k1", "acont", "account", 1},
		// k=2 match: two deletions
		{"short/match/k2", "acont", "account", 2},
		// k=2 no match: distance is 3
		{"short/nomatch/k2", "cont", "account", 2},
		// medium length strings
		{"medium/match/k1", "contrataicon", "contratacion", 1},
		{"medium/nomatch/k1", "conratcion", "contratacion", 1},
		{"medium/match/k2", "conratcion", "contratacion", 2},
		// long strings
		{"long/match/k1", "adjudicaion", "adjudicacion", 1},
		{"long/nomatch/k1", "adjudicaion", "licitacion", 1},
		// exact match (best case, should be fastest)
		{"exact/k1", "account", "account", 1},
		// completely different strings (worst case, full traversal)
		{"disjoint/k1", strings.Repeat("a", 10), strings.Repeat("z", 10), 1},
		{"disjoint/k2", strings.Repeat("a", 10), strings.Repeat("z", 10), 2},
	}

	for _, tc := range levenshteinCases {
		s1 := []byte(tc.s1)
		s2 := []byte(tc.s2)
		b.Run(tc.name, func(b *testing.B) {
			for b.Loop() {
				query.Levenshtein(s1, s2, tc.k)
			}
		})
	}
}
