package levenshtein_test

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"

	"github.com/RogueTeam/textiplex/levenshtein"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

// ── New: parameter validation ──────────────────────────────────────────────────

func TestNew(t *testing.T) {
	type Test struct {
		name    string
		k       int
		m       int
		keyword string
		wantNil bool
	}

	tests := []Test{
		{name: "valid k=0", k: 0, m: levenshtein.DefaultM, keyword: "contrato"},
		{name: "valid k=1", k: 1, m: levenshtein.DefaultM, keyword: "contrato"},
		{name: "valid k=3 upper bound", k: 3, m: levenshtein.DefaultM, keyword: "contrato"},
		{name: "valid unlimited m", k: 1, m: 0, keyword: "contrato"},
		{name: "valid keyword just below max length", k: 1, m: levenshtein.DefaultM, keyword: strings.Repeat("a", levenshtein.MaxLevenshteinLength-1)},
		{name: "negative k rejected", k: -1, m: levenshtein.DefaultM, keyword: "contrato", wantNil: true},
		{name: "k above 3 rejected", k: 4, m: levenshtein.DefaultM, keyword: "contrato", wantNil: true},
		{name: "keyword at max length rejected", k: 1, m: levenshtein.DefaultM, keyword: strings.Repeat("a", levenshtein.MaxLevenshteinLength), wantNil: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			automata := levenshtein.New(tc.k, tc.m, []byte(tc.keyword), testsuite.MakeTokenTree())

			if tc.wantNil {
				assertions.Nil(automata, "New must reject invalid parameters")
				return
			}
			assertions.NotNil(automata, "New must accept valid parameters")
		})
	}
}

// ── Start / Step / Accept / Dead: DP row mechanics ─────────────────────────────

func TestStateMechanics(t *testing.T) {
	t.Run("start row is identity clamped to k+1", func(t *testing.T) {
		assertions := assert.New(t)

		automata := levenshtein.New(1, 0, []byte("abcd"), testsuite.MakeTokenTree())
		if !assertions.NotNil(automata) {
			return
		}

		// keyword len 4, k=1 => [0,1,2,2,2] (cap = 2)
		assertions.Equal(levenshtein.State{0, 1, 2, 2, 2}, automata.Start(), "start row mismatch")
	})

	t.Run("stepping the exact keyword stays accepting", func(t *testing.T) {
		assertions := assert.New(t)

		keyword := "casa"
		automata := levenshtein.New(0, 0, []byte(keyword), testsuite.MakeTokenTree())
		if !assertions.NotNil(automata) {
			return
		}

		state := automata.Start()
		for i := 0; i < len(keyword); i++ {
			state = automata.Step(state, keyword[i])
			assertions.False(automata.Dead(state), "state dead after consuming %q", keyword[:i+1])
		}
		assertions.True(automata.Accept(state), "full keyword must be accepted at k=0")
	})

	t.Run("one substitution accepted at k=1 rejected at k=0", func(t *testing.T) {
		type Test struct {
			name       string
			k          int
			wantAccept bool
		}

		tests := []Test{
			{name: "k=0 rejects", k: 0, wantAccept: false},
			{name: "k=1 accepts", k: 1, wantAccept: true},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				assertions := assert.New(t)

				automata := levenshtein.New(tc.k, 0, []byte("casa"), testsuite.MakeTokenTree())
				if !assertions.NotNil(automata) {
					return
				}

				state := automata.Start()
				for _, c := range []byte("caza") { // one substitution
					state = automata.Step(state, c)
				}
				assertions.Equal(tc.wantAccept, automata.Accept(state), "accept mismatch for %q", "caza")
			})
		}
	})

	t.Run("diverging beyond k kills the state", func(t *testing.T) {
		assertions := assert.New(t)

		automata := levenshtein.New(1, 0, []byte("aaaa"), testsuite.MakeTokenTree())
		if !assertions.NotNil(automata) {
			return
		}

		state := automata.Start()
		for _, c := range []byte("zz") { // two leading substitutions exceed k=1
			state = automata.Step(state, c)
		}
		assertions.True(automata.Dead(state), "state must be dead after exceeding the edit budget")
	})
}

// ── Matches: matching set, ordering and m cap ──────────────────────────────────

func TestMatches(t *testing.T) {
	type Test struct {
		name    string
		k       int
		m       int
		keyword string
		terms   []string
		want    []string // expected yield, in order
	}

	tests := []Test{
		{
			name:    "exact match only at k=0",
			k:       0,
			m:       0,
			keyword: "contrato",
			terms:   []string{"contrato", "contratos", "contrata", "medellin"},
			want:    []string{"contrato"},
		},
		{
			name:    "substitution insertion and deletion at k=1",
			k:       1,
			m:       0,
			keyword: "contrato",
			terms: []string{
				"contrato",  // exact
				"contrata",  // substitution
				"contratos", // insertion
				"contrat",   // deletion
				"contratar", // distance 2: out
				"medellin",  // unrelated
			},
			want: []string{"contrat", "contrata", "contrato", "contratos"},
		},
		{
			name:    "distance two needs k=2",
			k:       2,
			m:       0,
			keyword: "bogota",
			terms:   []string{"bogota", "bogata", "bagata", "bxgxtx"},
			want:    []string{"bagata", "bogata", "bogota"},
		},
		{
			name:    "no candidates within distance",
			k:       1,
			m:       0,
			keyword: "zzz",
			terms:   []string{"contrato", "medellin", "bogota"},
			want:    []string{},
		},
		{
			name:    "empty tree yields nothing",
			k:       1,
			m:       0,
			keyword: "contrato",
			terms:   nil,
			want:    []string{},
		},
		{
			name:    "empty keyword matches terms of length up to k",
			k:       1,
			m:       0,
			keyword: "",
			terms:   []string{"", "a", "b", "ab"},
			want:    []string{"", "a", "b"},
		},
		{
			name:    "m caps the number of yielded terms",
			k:       1,
			m:       2,
			keyword: "casa",
			terms:   []string{"cama", "capa", "cara", "casa", "caza"},
			want:    []string{"cama", "capa"},
		},
		{
			name:    "m larger than matching set yields everything",
			k:       1,
			m:       100,
			keyword: "casa",
			terms:   []string{"cama", "casa", "perro"},
			want:    []string{"cama", "casa"},
		},
		{
			name:    "shared prefix family is walked without skipping",
			k:       1,
			m:       0,
			keyword: "inter",
			terms:   []string{"inta", "inte", "inter", "intera", "interv", "interventoria"},
			want:    []string{"inte", "inter", "intera", "interv"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			got := testsuite.CollectLevenshteinMatches(tc.k, tc.m, tc.keyword, tc.terms...)
			if !assertions.NotNil(got, "New rejected parameters that should be valid") {
				return
			}

			assertions.Equal(tc.want, got, "matching set mismatch")
			assertions.True(slices.IsSorted(got), "matches must be yielded in ascending byte order")
		})
	}
}

// ── Matches: early termination by the consumer ─────────────────────────────────

func TestMatchesEarlyBreak(t *testing.T) {
	assertions := assert.New(t)

	automata := levenshtein.New(1, 0, []byte("casa"), testsuite.MakeTokenTree("cama", "capa", "cara", "casa"))
	if !assertions.NotNil(automata) {
		return
	}

	count := 0
	for range automata.Matches() {
		count++
		break // production (clause.go) breaks after the first fuzzy hit
	}
	assertions.Equal(1, count, "breaking the range must stop the iterator after one yield")
}

// ── Matches: randomized trials against the brute-force oracle ──────────────────

func TestMatchesAgainstBruteForce(t *testing.T) {
	type Test struct {
		name     string
		seed     int64
		k        int
		m        int
		alphabet string
		maxLen   int
		corpus   int
		trials   int
	}

	tests := []Test{
		{name: "k=1 tight alphabet", seed: 1, k: 1, m: 0, alphabet: "ab", maxLen: 6, corpus: 200, trials: 50},
		{name: "k=1 wider alphabet", seed: 2, k: 1, m: 0, alphabet: "abcde", maxLen: 8, corpus: 300, trials: 50},
		{name: "k=2 tight alphabet", seed: 3, k: 2, m: 0, alphabet: "abc", maxLen: 7, corpus: 250, trials: 50},
		{name: "k=3 stress", seed: 4, k: 3, m: 0, alphabet: "ab", maxLen: 8, corpus: 250, trials: 30},
		{name: "k=1 with m cap", seed: 5, k: 1, m: 3, alphabet: "abc", maxLen: 6, corpus: 300, trials: 50},
		{name: "non ascii bytes", seed: 6, k: 1, m: 0, alphabet: "a\xff\x00z", maxLen: 5, corpus: 200, trials: 30},
	}

	randomTerm := func(rng *rand.Rand, alphabet string, maxLen int) string {
		var sb strings.Builder
		for range rng.Intn(maxLen + 1) {
			sb.WriteByte(alphabet[rng.Intn(len(alphabet))])
		}
		return sb.String()
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rng := rand.New(rand.NewSource(tc.seed))

			corpus := make([]string, 0, tc.corpus)
			for i := 0; i < tc.corpus; i++ {
				corpus = append(corpus, randomTerm(rng, tc.alphabet, tc.maxLen))
			}

			for trial := 0; trial < tc.trials; trial++ {
				keyword := randomTerm(rng, tc.alphabet, tc.maxLen)

				t.Run(fmt.Sprintf("trial_%d_kw_%x", trial, keyword), func(t *testing.T) {
					assertions := assert.New(t)

					got := testsuite.CollectLevenshteinMatches(tc.k, tc.m, keyword, corpus...)
					if !assertions.NotNil(got, "New rejected parameters that should be valid") {
						return
					}

					want := testsuite.BruteForceLevenshteinMatches(tc.k, tc.m, keyword, corpus...)
					assertions.Equal(want, got, "automaton diverged from brute force for keyword %q (k=%d m=%d)", keyword, tc.k, tc.m)
				})
			}
		})
	}
}

// ── LevenshteinDistance oracle sanity ──────────────────────────────────────────

func TestReferenceDistance(t *testing.T) {
	type Test struct {
		name string
		a    string
		b    string
		want int
	}

	tests := []Test{
		{name: "equal strings", a: "contrato", b: "contrato", want: 0},
		{name: "single substitution", a: "casa", b: "caza", want: 1},
		{name: "single insertion", a: "casa", b: "casas", want: 1},
		{name: "single deletion", a: "casa", b: "cas", want: 1},
		{name: "empty vs word", a: "", b: "abc", want: 3},
		{name: "classic kitten sitting", a: "kitten", b: "sitting", want: 3},
		{name: "symmetric", a: "sitting", b: "kitten", want: 3},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			assertions.Equal(tc.want, testsuite.LevenshteinDistance([]byte(tc.a), []byte(tc.b)), "distance(%q, %q)", tc.a, tc.b)
		})
	}
}
