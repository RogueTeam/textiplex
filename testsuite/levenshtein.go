package testsuite

import (
	"bytes"
	"slices"
	"strings"

	"github.com/RogueTeam/textiplex/levenshtein"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/tidwall/btree"
)

// MakeTokenTree builds a byte-sorted token BTree like the one Storage produces,
// using the same TokenLessFunc and NoLocks options as production. Duplicate
// terms collapse into a single key.
func MakeTokenTree(terms ...string) storage.Tokens {
	tree := btree.NewBTreeGOptions(func(a, b storage.Token) bool { return bytes.Compare(a.Value.Bytes(), b.Value.Bytes()) == -1 }, btree.Options{NoLocks: true})

	for _, term := range terms {
		tree.Set(storage.Token{Value: storage.TokenValueFrom(term)})
	}

	return tree.Items()
}

// CollectLevenshteinMatches builds a token tree from terms, runs the automaton
// for keyword/k/m and returns the matched values as strings, in the order the
// automaton yielded them. Values are copied out since Matches aliases tree keys.
// It returns nil when levenshtein.New rejects the parameters.
func CollectLevenshteinMatches(k, m int, keyword string, terms ...string) []string {
	automata := levenshtein.New(k, m, []byte(keyword), MakeTokenTree(terms...))
	if automata == nil {
		return nil
	}

	out := make([]string, 0)
	for token := range automata.Matches() {
		out = append(out, strings.Clone(token.Value.UnsafeString()))
	}
	return out
}

// LevenshteinDistance is a reference byte-level edit distance (insert, delete,
// substitute; all cost 1) used to verify the automaton against brute force.
func LevenshteinDistance(a, b []byte) int {
	prev := make([]int, len(b)+1)
	curr := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(a); i++ {
		curr[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min(prev[j-1]+cost, prev[j]+1, curr[j-1]+1)
		}
		prev, curr = curr, prev
	}
	return prev[len(b)]
}

// BruteForceLevenshteinMatches is the oracle counterpart of
// CollectLevenshteinMatches: it scans every term, keeps those within edit
// distance k of keyword, sorts them byte-ascending (the automaton's yield
// order) and caps the result at m (m <= 0 means unlimited).
func BruteForceLevenshteinMatches(k, m int, keyword string, terms ...string) []string {
	kw := []byte(keyword)

	unique := make(map[string]struct{}, len(terms))
	out := make([]string, 0)
	for _, term := range terms {
		if _, seen := unique[term]; seen {
			continue
		}
		unique[term] = struct{}{}
		if LevenshteinDistance(kw, []byte(term)) <= k {
			out = append(out, term)
		}
	}

	slices.Sort(out)
	if m > 0 && len(out) > m {
		out = out[:m]
	}
	return out
}
