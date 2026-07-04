package storage_test

import (
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

// mergeAndLoad runs Merger.Merge over a and b into a temp file, then loads the
// result back. The loaded storage is registered for cleanup via t.Cleanup so
// its mmap is released at test end.
func mergeAndLoad(t *testing.T, a, b *storage.Storage) *storage.Storage {
	t.Helper()
	assertions := assert.New(t)

	out := testsuite.TempFilename(t, "merge_*.bin")

	m := storage.Merger{TempDir: t.TempDir()}
	err := m.Merge(out, a, b)
	if !assertions.NoError(err, "merge must not fail") {
		t.FailNow()
	}

	var merged storage.Storage
	err = merged.Load(out)
	if !assertions.NoError(err, "loading merged file must not fail") {
		t.FailNow()
	}
	t.Cleanup(func() { merged.Close() })

	return &merged
}

// postingDocIDs returns the doc indices contained in a token's posting list.
func postingDocIDs(s *storage.Storage, tok *storage.Token) []uint32 {
	var bitmapForPostingListRetrieval roaring.Bitmap
	s.PostingLists[tok.PostingListIndex].UnsafeBitmap(&bitmapForPostingListRetrieval)

	return bitmapForPostingListRetrieval.ToArray()
}

// tokenFreqs returns the contiguous TF slice for a token.
func tokenFreqs(s *storage.Storage, tok *storage.Token) []storage.TokenFrequencyEntry {
	return s.TokenFrequencies[tok.FrequenciesIndex : tok.FrequenciesIndex+tok.FrequencyCount]
}

// getToken looks up a token by value in a field, failing the test if absent.
func getToken(t *testing.T, s *storage.Storage, fieldHash uint64, value string) *storage.Token {
	t.Helper()
	assertions := assert.New(t)

	field, ok := s.Fields[fieldHash]
	if !assertions.True(ok, "field %d must exist", fieldHash) {
		t.FailNow()
	}

	tok, ok := field.Tokens.GetString(value)
	if !assertions.True(ok, "token %q must exist in field %d", value, fieldHash) {
		t.FailNow()
	}
	return tok
}

// ── Doc ID concatenation and offsetting ───────────────────────────────────────

func TestMergeDocIDs(t *testing.T) {
	assertions := assert.New(t)

	// a holds the lexicographically-lower range, b the higher range, so the
	// disjoint ordered doc ID precondition holds.
	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("doc-0001", testsuite.MakeField(1, 1, testsuite.MakeToken("alpha", 1))),
		testsuite.MakeDoc("doc-0002", testsuite.MakeField(1, 1, testsuite.MakeToken("beta", 1))),
	)

	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("doc-0003", testsuite.MakeField(1, 1, testsuite.MakeToken("gamma", 1))),
		testsuite.MakeDoc("doc-0004", testsuite.MakeField(1, 1, testsuite.MakeToken("delta", 1))),
	)

	merged := mergeAndLoad(t, &a, &b)

	wantIDs := []string{"doc-0001", "doc-0002", "doc-0003", "doc-0004"}
	if !assertions.Len(merged.DocumentsIds, len(wantIDs)) {
		return
	}
	for i, want := range wantIDs {
		assertions.Equal(want, merged.DocumentsIds[i].Value.UnsafeString(), "doc id at %d", i)
	}
}

// ── Disjoint fields (A-only and B-only) ───────────────────────────────────────

func TestMergeDisjointFields(t *testing.T) {
	assertions := assert.New(t)

	// Field 1 lives only in a, field 2 only in b. No collisions.
	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 2,
			testsuite.MakeToken("contrato", 2),
		)),
	)

	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("doc-b", testsuite.MakeField(2, 3,
			testsuite.MakeToken("interventoria", 3),
		)),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Len(merged.Fields, 2, "both disjoint fields must survive")

	// Field 1 token unchanged, doc index 0 (from a).
	tokA := getToken(t, merged, 1, "contrato")
	assertions.Equal(uint64(1), tokA.FrequencyCount)
	assertions.Equal([]uint32{0}, postingDocIDs(merged, tokA))
	assertions.Equal(
		[]storage.TokenFrequencyEntry{{DocumentIndex: 0, Frequency: 2}},
		tokenFreqs(merged, tokA),
	)

	// Field 2 token from b, doc index offset by len(a.DocumentsIds) == 1.
	tokB := getToken(t, merged, 2, "interventoria")
	assertions.Equal(uint64(1), tokB.FrequencyCount)
	assertions.Equal([]uint32{1}, postingDocIDs(merged, tokB), "b doc id must be offset")
	assertions.Equal(
		[]storage.TokenFrequencyEntry{{DocumentIndex: 1, Frequency: 3}},
		tokenFreqs(merged, tokB),
	)
}

// ── Collision field, disjoint tokens ──────────────────────────────────────────

func TestMergeCollisionFieldDisjointTokens(t *testing.T) {
	assertions := assert.New(t)

	// Same field hash 1 in both, but no shared token values.
	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 4,
			testsuite.MakeToken("bogota", 4),
		)),
	)

	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("doc-b", testsuite.MakeField(1, 2,
			testsuite.MakeToken("medellin", 2),
		)),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Len(merged.Fields, 1, "collision field must be merged into one")

	field := merged.Fields[1]
	assertions.Equal(2, len(field.Tokens), "both tokens present in merged field")

	// avgdl recomputed over both docs: (4 + 2) / 2 == 3.
	assertions.InDelta(3.0, field.AvgDocumentLength, 0.0001)

	// a token keeps doc 0.
	tokA := getToken(t, merged, 1, "bogota")
	assertions.Equal([]uint32{0}, postingDocIDs(merged, tokA))

	// b token offset to doc 1.
	tokB := getToken(t, merged, 1, "medellin")
	assertions.Equal([]uint32{1}, postingDocIDs(merged, tokB))

	// Doc lengths: a's verbatim, b's offset.
	assertions.Equal(
		[]storage.DocumentLengthEntry{
			{Index: 0, Length: 4},
			{Index: 1, Length: 2},
		},
		field.DocumentLengths,
	)
}

// ── Collision field, shared token (the hard path) ─────────────────────────────

func TestMergeCollisionFieldSharedToken(t *testing.T) {
	assertions := assert.New(t)

	// "contrato" appears in both a and b under the same field hash.
	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("doc-a1", testsuite.MakeField(1, 3,
			testsuite.MakeToken("contrato", 3),
		)),
		testsuite.MakeDoc("doc-a2", testsuite.MakeField(1, 1,
			testsuite.MakeToken("contrato", 1),
		)),
	)

	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("doc-b1", testsuite.MakeField(1, 2,
			testsuite.MakeToken("contrato", 2),
		)),
	)

	merged := mergeAndLoad(t, &a, &b)

	assertions.Len(merged.Fields, 1)
	assertions.Len(merged.DocumentsIds, 3)

	tok := getToken(t, merged, 1, "contrato")

	// doc freq is the sum: 2 (from a) + 1 (from b) == 3.
	assertions.Equal(uint64(3), tok.FrequencyCount, "merged doc freq must sum both sides")

	// Posting list is the union with b's doc shifted by docOffset (2).
	// a docs: 0, 1.  b doc 0 -> 0 + 2 == 2.
	assertions.Equal([]uint32{0, 1, 2}, postingDocIDs(merged, tok))

	// TF entries: a's verbatim then b's offset, ascending by doc index.
	assertions.Equal(
		[]storage.TokenFrequencyEntry{
			{DocumentIndex: 0, Frequency: 3},
			{DocumentIndex: 1, Frequency: 1},
			{DocumentIndex: 2, Frequency: 2},
		},
		tokenFreqs(merged, tok),
	)

	// avgdl over all three docs: (3 + 1 + 2) / 3 == 2.
	field := merged.Fields[1]
	assertions.InDelta(2.0, field.AvgDocumentLength, 0.0001)
}

// ── Mixed: A-only field, B-only field, and a collision field together ─────────

func TestMergeMixed(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("doc-b",
			testsuite.MakeField(1, 2, testsuite.MakeToken("aonly", 2)),                                   // A-only field
			testsuite.MakeField(3, 3, testsuite.MakeToken("shared", 1), testsuite.MakeToken("aside", 2)), // collision field
		),
	)

	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("doc-a",
			testsuite.MakeField(2, 1, testsuite.MakeToken("bonly", 1)),                                   // B-only field
			testsuite.MakeField(3, 2, testsuite.MakeToken("shared", 1), testsuite.MakeToken("bside", 1)), // collision field
		),
	)

	merged := mergeAndLoad(t, &a, &b)

	// Fields: 1 (A-only), 2 (B-only), 3 (collision) == 3 total.
	assertions.Len(merged.Fields, 3)

	// A-only field 1, doc 0.
	tok1 := getToken(t, merged, 1, "aonly")
	assertions.Equal([]uint32{0}, postingDocIDs(merged, tok1))

	// B-only field 2, doc offset to 1.
	tok2 := getToken(t, merged, 2, "bonly")
	assertions.Equal([]uint32{1}, postingDocIDs(merged, tok2))

	// Collision field 3.
	field3 := merged.Fields[3]
	assertions.Equal(3, len(field3.Tokens), "shared, aside, bside")

	shared := getToken(t, merged, 3, "shared")
	assertions.Equal(uint64(2), shared.FrequencyCount)
	assertions.Equal([]uint32{0, 1}, postingDocIDs(merged, shared))

	aside := getToken(t, merged, 3, "aside")
	assertions.Equal([]uint32{0}, postingDocIDs(merged, aside))

	bside := getToken(t, merged, 3, "bside")
	assertions.Equal([]uint32{1}, postingDocIDs(merged, bside))
}

// ── Header counts ─────────────────────────────────────────────────────────────

func TestMergeHeaderCounts(t *testing.T) {
	assertions := assert.New(t)

	var a storage.Storage
	a.BuildFrom(
		testsuite.MakeDoc("doc-a",
			testsuite.MakeField(1, 1, testsuite.MakeToken("x", 1)),
			testsuite.MakeField(3, 1, testsuite.MakeToken("shared", 1)),
		),
	)

	var b storage.Storage
	b.BuildFrom(
		testsuite.MakeDoc("doc-b",
			testsuite.MakeField(2, 1, testsuite.MakeToken("y", 1)),
			testsuite.MakeField(3, 1, testsuite.MakeToken("shared", 1)),
		),
	)

	merged := mergeAndLoad(t, &a, &b)

	// 2 docs total.
	assertions.Len(merged.DocumentsIds, 2)
	// Fields 1, 2, 3 -> 3.
	assertions.Len(merged.Fields, 3)

	// Posting lists: field1 token x (1), field2 token y (1), field3 token shared
	// merged into a single posting list (1). Total 3.
	assertions.Len(merged.PostingLists, 3)

	// Token frequencies: x(1) + y(1) + shared merged(2) == 4.
	assertions.Len(merged.TokenFrequencies, 4)

}

// ── Empty operand merges ──────────────────────────────────────────────────────

func TestMergeWithEmpty(t *testing.T) {
	t.Run("b empty", func(t *testing.T) {
		assertions := assert.New(t)

		var a storage.Storage
		a.BuildFrom(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 1, testsuite.MakeToken("only", 1))),
		)
		var b storage.Storage
		b.ColdInitialize() // empty, initialized

		merged := mergeAndLoad(t, &a, &b)

		assertions.Len(merged.DocumentsIds, 1)
		assertions.Len(merged.Fields, 1)
		tok := getToken(t, merged, 1, "only")
		assertions.Equal([]uint32{0}, postingDocIDs(merged, tok))
	})

	t.Run("a empty", func(t *testing.T) {
		assertions := assert.New(t)

		var a storage.Storage
		a.ColdInitialize() // empty
		var b storage.Storage
		b.BuildFrom(
			testsuite.MakeDoc("doc-b", testsuite.MakeField(1, 1, testsuite.MakeToken("only", 1))),
		)

		merged := mergeAndLoad(t, &a, &b)

		assertions.Len(merged.DocumentsIds, 1)
		assertions.Len(merged.Fields, 1)
		tok := getToken(t, merged, 1, "only")
		// a is empty so docOffset == 0, b's doc stays at index 0.
		assertions.Equal([]uint32{0}, postingDocIDs(merged, tok))
	})
}
