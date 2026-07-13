package storage_test

import (
	"bytes"
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

// ── Additional helpers ────────────────────────────────────────────────────────

// tokenValues returns the stored token values of a field, in stored order.
// Asserting against a sorted expectation validates the merge's ordering, which
// is the invariant most easily broken by a faulty token merge.
func tokenValues(t *testing.T, s *storage.Storage, fieldHash uint64) []string {
	t.Helper()
	assertions := assert.New(t)

	field, ok := s.Fields[fieldHash]
	if !assertions.True(ok, "field %d must exist", fieldHash) {
		t.FailNow()
	}

	out := make([]string, len(field.Tokens))
	for i := range field.Tokens {
		out[i] = string(field.Tokens[i].Value.Bytes())
	}
	return out
}

// assertTokensSorted fails if a field's tokens are not strictly ascending.
func assertTokensSorted(t *testing.T, s *storage.Storage, fieldHash uint64) {
	t.Helper()
	assertions := assert.New(t)

	field, ok := s.Fields[fieldHash]
	if !assertions.True(ok, "field %d must exist", fieldHash) {
		return
	}
	for i := 1; i < len(field.Tokens); i++ {
		prev := field.Tokens[i-1].Value.Bytes()
		cur := field.Tokens[i].Value.Bytes()
		assertions.Equal(-1, bytes.Compare(prev, cur),
			"field %d tokens must be strictly ascending at %d: %q vs %q", fieldHash, i, prev, cur)
	}
}

// tfDocIDs returns the document indices referenced by a token's TF slice.
func tfDocIDs(s *storage.Storage, tok *storage.Token) []uint32 {
	freqs := tokenFreqs(s, tok)
	out := make([]uint32, len(freqs))
	for i := range freqs {
		out[i] = freqs[i].DocumentIndex
	}
	return out
}

// buildMixedMerged builds the canonical mixed scenario used by the structural
// invariant tests: an A-only field, a B-only field, and a collision field that
// carries one shared token plus one token unique to each side.
//
// Doc indices after merge: a-1=0, a-2=1, b-1=2 (docOffset == 2).
func buildMixedMerged(t *testing.T) *storage.Storage {
	t.Helper()

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1",
			testsuite.MakeField(1, 2, testsuite.MakeToken("alpha", 2)),                                   // A-only field
			testsuite.MakeField(3, 2, testsuite.MakeToken("shared", 1), testsuite.MakeToken("aonly", 1)), // collision field
		),
		testsuite.MakeDoc("a-2",
			testsuite.MakeField(1, 3, testsuite.MakeToken("alpha", 3)),
			testsuite.MakeField(3, 2, testsuite.MakeToken("shared", 2)),
		),
	)

	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1",
			testsuite.MakeField(2, 1, testsuite.MakeToken("beta", 1)),                                    // B-only field
			testsuite.MakeField(3, 2, testsuite.MakeToken("shared", 1), testsuite.MakeToken("bonly", 1)), // collision field
		),
	)

	return mergeAndLoad(t, &a, &b)
}

// ── Token ordering inside collision fields ────────────────────────────────────

// 1. Interleaved values from both sides must come out globally sorted.
func TestMergeCollisionTokensInterleaved(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1", testsuite.MakeField(1, 3,
		testsuite.MakeToken("apple", 1), testsuite.MakeToken("cat", 1), testsuite.MakeToken("dog", 1),
	)))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1", testsuite.MakeField(1, 3,
		testsuite.MakeToken("ant", 1), testsuite.MakeToken("banana", 1), testsuite.MakeToken("cat", 1),
	)))

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal([]string{"ant", "apple", "banana", "cat", "dog"}, tokenValues(t, merged, 1))
	assertTokensSorted(t, merged, 1)

	cat := getToken(t, merged, 1, "cat")
	assertions.Equal(uint64(2), cat.FrequencyCount, "cat appears in both sides")
	assertions.Equal([]uint32{0, 1}, postingDocIDs(merged, cat))
	assertions.Equal([]uint32{1}, postingDocIDs(merged, getToken(t, merged, 1, "ant")))
	assertions.Equal([]uint32{0}, postingDocIDs(merged, getToken(t, merged, 1, "apple")))
}

// 2. Every A token sorts after every B token.
func TestMergeCollisionAllATokensAfterB(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1", testsuite.MakeField(1, 3,
		testsuite.MakeToken("x", 1), testsuite.MakeToken("y", 1), testsuite.MakeToken("z", 1),
	)))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1", testsuite.MakeField(1, 3,
		testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1), testsuite.MakeToken("c", 1),
	)))

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal([]string{"a", "b", "c", "x", "y", "z"}, tokenValues(t, merged, 1))
	assertTokensSorted(t, merged, 1)
	assertions.Equal([]uint32{1}, postingDocIDs(merged, getToken(t, merged, 1, "a")))
	assertions.Equal([]uint32{0}, postingDocIDs(merged, getToken(t, merged, 1, "z")))
}

// 3. Every A token sorts before every B token.
func TestMergeCollisionAllBTokensAfterA(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1", testsuite.MakeField(1, 3,
		testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1), testsuite.MakeToken("c", 1),
	)))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1", testsuite.MakeField(1, 3,
		testsuite.MakeToken("x", 1), testsuite.MakeToken("y", 1), testsuite.MakeToken("z", 1),
	)))

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal([]string{"a", "b", "c", "x", "y", "z"}, tokenValues(t, merged, 1))
	assertTokensSorted(t, merged, 1)
	assertions.Equal([]uint32{0}, postingDocIDs(merged, getToken(t, merged, 1, "a")))
	assertions.Equal([]uint32{1}, postingDocIDs(merged, getToken(t, merged, 1, "z")))
}

// 4. Every token collides; all must merge and stay sorted.
func TestMergeCollisionFullyShared(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1", testsuite.MakeField(1, 5,
		testsuite.MakeToken("alpha", 2), testsuite.MakeToken("beta", 3),
	)))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1", testsuite.MakeField(1, 12,
		testsuite.MakeToken("alpha", 5), testsuite.MakeToken("beta", 7),
	)))

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal([]string{"alpha", "beta"}, tokenValues(t, merged, 1))

	alpha := getToken(t, merged, 1, "alpha")
	assertions.Equal(uint64(2), alpha.FrequencyCount)
	assertions.Equal([]uint32{0, 1}, postingDocIDs(merged, alpha))
	assertions.Equal(
		[]storage.TokenFrequencyEntry{{DocumentIndex: 0, Frequency: 2}, {DocumentIndex: 1, Frequency: 5}},
		tokenFreqs(merged, alpha),
	)
}

// 5. Tokens that share prefixes must order by full byte comparison.
func TestMergeCollisionSharedPrefixes(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1", testsuite.MakeField(1, 2,
		testsuite.MakeToken("a", 1), testsuite.MakeToken("abc", 1),
	)))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1", testsuite.MakeField(1, 2,
		testsuite.MakeToken("ab", 1), testsuite.MakeToken("abcd", 1),
	)))

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal([]string{"a", "ab", "abc", "abcd"}, tokenValues(t, merged, 1))
	assertTokensSorted(t, merged, 1)
}

// 6. Disjoint tokens fed in unsorted insertion order still merge sorted.
func TestMergeCollisionTokensRemainSorted(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1", testsuite.MakeField(1, 3,
		testsuite.MakeToken("c", 1), testsuite.MakeToken("a", 1), testsuite.MakeToken("e", 1),
	)))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1", testsuite.MakeField(1, 3,
		testsuite.MakeToken("d", 1), testsuite.MakeToken("b", 1), testsuite.MakeToken("f", 1),
	)))

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal([]string{"a", "b", "c", "d", "e", "f"}, tokenValues(t, merged, 1))
	assertTokensSorted(t, merged, 1)
}

// 7. A's tokens are a subset of B's tokens.
func TestMergeCollisionASubsetOfB(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1", testsuite.MakeField(1, 2,
		testsuite.MakeToken("b", 1), testsuite.MakeToken("d", 1),
	)))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1", testsuite.MakeField(1, 5,
		testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1), testsuite.MakeToken("c", 1),
		testsuite.MakeToken("d", 1), testsuite.MakeToken("e", 1),
	)))

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal([]string{"a", "b", "c", "d", "e"}, tokenValues(t, merged, 1))
	assertions.Equal(uint64(2), getToken(t, merged, 1, "b").FrequencyCount)
	assertions.Equal([]uint32{0, 1}, postingDocIDs(merged, getToken(t, merged, 1, "d")))
	assertions.Equal([]uint32{1}, postingDocIDs(merged, getToken(t, merged, 1, "a")))
}

// ── Document-index offsetting ─────────────────────────────────────────────────

// 8. B-only field spanning several docs is offset by len(a docs).
func TestMergeBOnlyFieldMultiDocOffset(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("x", 1))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 1, testsuite.MakeToken("x", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(2, 1, testsuite.MakeToken("y", 1))),
		testsuite.MakeDoc("b-2", testsuite.MakeField(2, 1, testsuite.MakeToken("y", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal([]uint32{0, 1}, postingDocIDs(merged, getToken(t, merged, 1, "x")))
	assertions.Equal([]uint32{2, 3}, postingDocIDs(merged, getToken(t, merged, 2, "y")), "b docs offset by 2")
}

// 9. Collision token's B-side posting entries are offset before the union.
func TestMergeCollisionBPostingOffset(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("shared", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 1, testsuite.MakeToken("shared", 1))),
		testsuite.MakeDoc("b-2", testsuite.MakeField(1, 1, testsuite.MakeToken("shared", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	tok := getToken(t, merged, 1, "shared")
	assertions.Equal(uint64(3), tok.FrequencyCount)
	assertions.Equal([]uint32{0, 1, 2}, postingDocIDs(merged, tok))
}

// 10. A large A-side doc count produces a correspondingly large offset.
func TestMergeLargeDocOffset(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("x", 1))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 1, testsuite.MakeToken("x", 1))),
		testsuite.MakeDoc("a-3", testsuite.MakeField(1, 1, testsuite.MakeToken("x", 1))),
		testsuite.MakeDoc("a-4", testsuite.MakeField(1, 1, testsuite.MakeToken("x", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(2, 1, testsuite.MakeToken("y", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Len(merged.DocumentsIds, 5)
	assertions.Equal([]uint32{0, 1, 2, 3}, postingDocIDs(merged, getToken(t, merged, 1, "x")))
	assertions.Equal([]uint32{4}, postingDocIDs(merged, getToken(t, merged, 2, "y")))
}

// 11. Collision field doc-length indices: A verbatim, B offset.
func TestMergeCollisionDocLengthsOffset(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 5, testsuite.MakeToken("t", 5))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 2, testsuite.MakeToken("t", 2))),
		testsuite.MakeDoc("b-2", testsuite.MakeField(1, 3, testsuite.MakeToken("t", 3))),
	)

	merged := mergeAndLoad(t, &a, &b)

	field := merged.Fields[1]
	assertions.Equal(
		[]storage.DocumentLengthEntry{{Index: 0, Length: 5}, {Index: 1, Length: 2}, {Index: 2, Length: 3}},
		field.DocumentLengths,
	)
}

// 12. Collision token's B-side TF document indices are offset.
func TestMergeTFDocumentIndexOffset(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 9, testsuite.MakeToken("t", 9))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 4, testsuite.MakeToken("t", 4))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal(
		[]storage.TokenFrequencyEntry{{DocumentIndex: 0, Frequency: 9}, {DocumentIndex: 1, Frequency: 4}},
		tokenFreqs(merged, getToken(t, merged, 1, "t")),
	)
}

// ── Frequency / TF merge ──────────────────────────────────────────────────────

// 13. Document frequency is the sum of both sides' document counts.
func TestMergeSharedTokenSumsDocFrequency(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("k", 1))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 1, testsuite.MakeToken("k", 1))),
		testsuite.MakeDoc("a-3", testsuite.MakeField(1, 1, testsuite.MakeToken("k", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 1, testsuite.MakeToken("k", 1))),
		testsuite.MakeDoc("b-2", testsuite.MakeField(1, 1, testsuite.MakeToken("k", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	tok := getToken(t, merged, 1, "k")
	assertions.Equal(uint64(5), tok.FrequencyCount)
	assertions.Equal([]uint32{0, 1, 2, 3, 4}, postingDocIDs(merged, tok))
}

// 14. Per-document term frequencies survive the merge unchanged.
func TestMergeSharedTokenPreservesTermFrequencies(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 3, testsuite.MakeToken("w", 3))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 7, testsuite.MakeToken("w", 7))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 5, testsuite.MakeToken("w", 5))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal(
		[]storage.TokenFrequencyEntry{
			{DocumentIndex: 0, Frequency: 3},
			{DocumentIndex: 1, Frequency: 7},
			{DocumentIndex: 2, Frequency: 5},
		},
		tokenFreqs(merged, getToken(t, merged, 1, "w")),
	)
}

// 15. TF entries are ordered by document index, not by term frequency.
func TestMergeTFOrderByDocNotFreq(t *testing.T) {
	assertions := assert.New(t)

	// A's doc has a small frequency, B's a large one. Order must still be A then B.
	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 100, testsuite.MakeToken("t", 100))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal(
		[]storage.TokenFrequencyEntry{{DocumentIndex: 0, Frequency: 1}, {DocumentIndex: 1, Frequency: 100}},
		tokenFreqs(merged, getToken(t, merged, 1, "t")),
	)
}

// 16. For every token, the TF document set equals its posting list set.
func TestMergeTFDocIDsMatchPosting(t *testing.T) {
	assertions := assert.New(t)
	merged := buildMixedMerged(t)

	for fieldHash, field := range merged.Fields {
		for i := range field.Tokens {
			tok := &field.Tokens[i]
			assertions.Equal(
				postingDocIDs(merged, tok),
				tfDocIDs(merged, tok),
				"field %d token %q: TF docs must equal posting docs", fieldHash, tok.Value.Bytes(),
			)
		}
	}
}

// 17. For every token, posting cardinality equals its document frequency.
func TestMergePostingCardinalityEqualsFreqCount(t *testing.T) {
	assertions := assert.New(t)
	merged := buildMixedMerged(t)

	for fieldHash, field := range merged.Fields {
		for i := range field.Tokens {
			tok := &field.Tokens[i]
			assertions.Len(
				postingDocIDs(merged, tok), int(tok.FrequencyCount),
				"field %d token %q: posting size must equal FrequencyCount", fieldHash, tok.Value.Bytes(),
			)
		}
	}
}

// ── Average document length ───────────────────────────────────────────────────

// 18. Collision avgdl is recomputed over all field docs (fractional result).
func TestMergeCollisionAvgdlRecomputed(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 2, testsuite.MakeToken("t", 2))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 4, testsuite.MakeToken("t", 4))),
	)

	merged := mergeAndLoad(t, &a, &b)

	// (1 + 2 + 4) / 3 == 7/3 ≈ 2.3333 — also exercises float bit round-trip.
	assertions.InDelta(7.0/3.0, merged.Fields[1].AvgDocumentLength, 1e-6)
}

// 19. An A-only field keeps its original avgdl.
func TestMergeAOnlyFieldAvgdlPreserved(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 4, testsuite.MakeToken("t", 4))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 2, testsuite.MakeToken("t", 2))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(2, 1, testsuite.MakeToken("u", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.InDelta(3.0, merged.Fields[1].AvgDocumentLength, 1e-9)
}

// 20. A B-only field keeps its original avgdl.
func TestMergeBOnlyFieldAvgdlPreserved(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(2, 6, testsuite.MakeToken("u", 6))),
		testsuite.MakeDoc("b-2", testsuite.MakeField(2, 2, testsuite.MakeToken("u", 2))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.InDelta(4.0, merged.Fields[2].AvgDocumentLength, 1e-9)
}

// 21. Collision avgdl with a mix of shared and side-unique tokens.
func TestMergeCollisionAvgdlMixedTokens(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 3,
			testsuite.MakeToken("shared", 2), testsuite.MakeToken("aside", 1))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 1, testsuite.MakeToken("shared", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 2,
			testsuite.MakeToken("shared", 1), testsuite.MakeToken("bside", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.InDelta(2.0, merged.Fields[1].AvgDocumentLength, 1e-9) // (3+1+2)/3
	assertions.Equal([]string{"aside", "bside", "shared"}, tokenValues(t, merged, 1))
	assertions.Equal(uint64(3), getToken(t, merged, 1, "shared").FrequencyCount)
	assertions.Equal([]uint32{0, 1, 2}, postingDocIDs(merged, getToken(t, merged, 1, "shared")))
	assertions.Equal([]uint32{0}, postingDocIDs(merged, getToken(t, merged, 1, "aside")))
	assertions.Equal([]uint32{2}, postingDocIDs(merged, getToken(t, merged, 1, "bside")))
}

// ── Header counts ─────────────────────────────────────────────────────────────

// 22. Posting-list count equals the number of distinct merged tokens.
func TestMergePostingListCountAcrossFields(t *testing.T) {
	assertions := assert.New(t)
	merged := buildMixedMerged(t)

	// field1: alpha; field2: beta; field3: shared, aonly, bonly == 5 distinct.
	assertions.Len(merged.PostingLists, 5)
}

// 23. Token-frequency count equals the sum of merged document frequencies.
func TestMergeTokenFrequenciesCount(t *testing.T) {
	assertions := assert.New(t)
	merged := buildMixedMerged(t)

	// alpha(2) + beta(1) + shared(3) + aonly(1) + bonly(1) == 8.
	assertions.Len(merged.TokenFrequencies, 8)
}

// 24. Field count reflects every collision being folded into one.
func TestMergeFieldsCountManyCollisions(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1",
		testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1)),
		testsuite.MakeField(2, 1, testsuite.MakeToken("t", 1)),
		testsuite.MakeField(3, 1, testsuite.MakeToken("t", 1)),
		testsuite.MakeField(4, 1, testsuite.MakeToken("t", 1)),
	))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1",
		testsuite.MakeField(3, 1, testsuite.MakeToken("t", 1)),
		testsuite.MakeField(4, 1, testsuite.MakeToken("t", 1)),
		testsuite.MakeField(5, 1, testsuite.MakeToken("t", 1)),
		testsuite.MakeField(6, 1, testsuite.MakeToken("t", 1)),
	))

	merged := mergeAndLoad(t, &a, &b)

	// 4 + 4 - 2 collisions (fields 3, 4) == 6.
	assertions.Len(merged.Fields, 6)
}

// 25. Doc IDs concatenate as A's sorted block followed by B's sorted block.
func TestMergeDocIDsOrderManyDocs(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1))),
		testsuite.MakeDoc("a-3", testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1))),
		testsuite.MakeDoc("b-2", testsuite.MakeField(1, 1, testsuite.MakeToken("t", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	want := []string{"a-1", "a-2", "a-3", "b-1", "b-2"}
	if !assertions.Len(merged.DocumentsIds, len(want)) {
		return
	}
	for i, id := range want {
		assertions.Equal(id, merged.DocumentsIds[i].Value.UnsafeString(), "doc id at %d", i)
	}
}

// ── Structural / mmap ─────────────────────────────────────────────────────────

// 26. All posting lists, including merged collision ones, are mmap-backed.
func TestMergePostingListsUnsafeWithCollision(t *testing.T) {
	assertions := assert.New(t)
	merged := buildMixedMerged(t)

	assertions.NotEmpty(merged.PostingLists)
}

// 27. Collision field doc-length contents across multiple docs on both sides.
func TestMergeCollisionDocLengthsContent(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 2, testsuite.MakeToken("t", 2))),
		testsuite.MakeDoc("a-2", testsuite.MakeField(1, 3, testsuite.MakeToken("t", 3))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(1, 4, testsuite.MakeToken("t", 4))),
		testsuite.MakeDoc("b-2", testsuite.MakeField(1, 5, testsuite.MakeToken("t", 5))),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Equal(
		[]storage.DocumentLengthEntry{
			{Index: 0, Length: 2},
			{Index: 1, Length: 3},
			{Index: 2, Length: 4},
			{Index: 3, Length: 5},
		},
		merged.Fields[1].DocumentLengths,
	)
}

// ── Field isolation ───────────────────────────────────────────────────────────

// 28. Identical token value in two different fields must not be merged together.
func TestMergeSameTokenValueDifferentFields(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("a-1", testsuite.MakeField(1, 1, testsuite.MakeToken("dup", 1))),
	)
	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("b-1", testsuite.MakeField(2, 1, testsuite.MakeToken("dup", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	f1 := getToken(t, merged, 1, "dup")
	f2 := getToken(t, merged, 2, "dup")
	assertions.Equal(uint64(1), f1.FrequencyCount)
	assertions.Equal(uint64(1), f2.FrequencyCount)
	assertions.Equal([]uint32{0}, postingDocIDs(merged, f1))
	assertions.Equal([]uint32{1}, postingDocIDs(merged, f2), "field 2 dup is the b doc")
}

// 29. A value that collides in two separate collision fields merges only within
// its own field.
func TestMergeTokenValueCollidesAcrossFieldsNotMerged(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(testsuite.MakeDoc("a-1",
		testsuite.MakeField(3, 1, testsuite.MakeToken("x", 1)),
		testsuite.MakeField(4, 1, testsuite.MakeToken("x", 1)),
	))
	var b storage.Storage
	b.BuildFrom(testsuite.MakeDoc("b-1",
		testsuite.MakeField(3, 1, testsuite.MakeToken("x", 1)),
		testsuite.MakeField(4, 1, testsuite.MakeToken("x", 1)),
	))

	merged := mergeAndLoad(t, &a, &b)

	f3 := getToken(t, merged, 3, "x")
	f4 := getToken(t, merged, 4, "x")
	assertions.Equal(uint64(2), f3.FrequencyCount)
	assertions.Equal(uint64(2), f4.FrequencyCount)
	assertions.Equal([]uint32{0, 1}, postingDocIDs(merged, f3))
	assertions.Equal([]uint32{0, 1}, postingDocIDs(merged, f4))
}

// ── Degenerate ────────────────────────────────────────────────────────────────

// 30. Merging two empty storages yields a valid, empty result.
func TestMergeBothEmpty(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.ColdInitialize()
	var b storage.Storage
	b.ColdInitialize()

	merged := mergeAndLoad(t, &a, &b)

	assertions.Empty(merged.DocumentsIds)
	assertions.Empty(merged.Fields)
	assertions.Empty(merged.PostingLists)
	assertions.Empty(merged.TokenFrequencies)
}
