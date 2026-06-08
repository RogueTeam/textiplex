package levenshtein_test

import (
	"testing"

	"github.com/RogueTeam/textiplex/levenshtein"
	"github.com/stretchr/testify/assert"
)

func TestLevenshtein(t *testing.T) {
	cases := []struct {
		name   string
		s1, s2 string
		k      int
		expect bool
	}{
		// --- Exact match ---
		{"exact/empty", "", "", 1, true},
		{"exact/single", "a", "a", 1, true},
		{"exact/word", "account", "account", 1, true},
		{"exact/long", "contratacion", "contratacion", 1, true},

		// --- Deletion (char in s2 missing from s1) ---
		{"deletion/leading", "count", "acount", 1, true},
		{"deletion/middle", "acont", "acount", 1, true},
		{"deletion/trailing", "accoun", "account", 1, true},
		{"deletion/two/k1", "acont", "account", 1, false},
		{"deletion/two/k2", "acont", "account", 2, true},

		// --- Insertion (extra char in s1 not in s2) ---
		{"insertion/leading", "aacount", "acount", 1, true},
		{"insertion/middle", "account", "acount", 1, true},
		{"insertion/trailing", "acountt", "acount", 1, true},
		{"insertion/two/k1", "aaccount", "acount", 1, false},
		{"insertion/two/k2", "aaccount", "acount", 2, true},

		// --- Substitution ---
		{"substitution/leading", "bcount", "acount", 1, true},
		{"substitution/middle", "acxunt", "acount", 1, true},
		{"substitution/trailing", "acounx", "acount", 1, true},
		{"substitution/two/k1", "bcxunt", "acount", 1, false},
		{"substitution/two/k2", "bcxunt", "acount", 2, true},

		// --- Transposition (swap adjacent chars) ---
		// transposition = 2 ops (sub+sub), so costs 2 under basic Levenshtein
		{"transposition/k1", "acuont", "acount", 1, false},
		{"transposition/k2", "acuont", "acount", 2, true},

		// --- No match ---
		{"nomatch/disjoint", "zzz", "aaa", 1, false},
		{"nomatch/disjoint/k2", "zzzzz", "aaaaa", 2, false},
		{"nomatch/completely_different", "xyz", "account", 1, false},

		// --- Empty string edge cases ---
		{"empty/s1_empty/k1", "", "a", 1, true},
		{"empty/s1_empty/k2", "", "ab", 1, false},
		{"empty/s1_empty/k2/pass", "", "ab", 2, true},
		{"empty/s2_empty/k1", "a", "", 1, true},
		{"empty/both_empty/k0", "", "", 0, true},

		// --- k=0 (exact match only) ---
		{"k0/exact", "account", "account", 0, true},
		{"k0/nomatch", "acount", "account", 0, false},

		// --- Spanish procurement terms (real BuscaSECOP vocabulary) ---
		{"spanish/contratacion/del", "contratacion", "contrataciion", 1, true},
		{"spanish/licitacion/sub", "licitacion", "licitasion", 1, true},
		{"spanish/adjudicacion/del", "adjudicacion", "adjudicacion", 1, true},
		{"spanish/interventoria/ins", "interventoriaa", "interventoria", 1, true},
		{"spanish/two_errors/k1", "lictacion", "licitacion", 1, true},
		{"spanish/two_errors/k2", "lictacion", "licitacion", 2, true},

		// --- Length mismatch edge cases ---
		{"length/s1_much_longer", "accountxxx", "account", 1, false},
		{"length/s1_much_longer/k3", "accountxxx", "account", 3, true},
		{"length/s2_much_longer", "account", "accountxxx", 1, false},
		{"length/s2_much_longer/k3", "account", "accountxxx", 3, true},
		{"length/single_vs_empty/k0", "a", "", 0, false},
		{"length/single_vs_empty/k1", "a", "", 1, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			match := levenshtein.LevenshteinMatch([]byte(tc.s1), []byte(tc.s2), tc.k)
			if !assertions.Equal(tc.expect, match, "match should be equal to expected") {
				return
			}
		})
	}
}
