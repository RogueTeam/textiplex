package query_test

import (
	"slices"
	"testing"

	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

// ══════════════════════════════════════════════════════════════════════════════
// FieldScore — token-order ranking
//
// FieldScore ranks the candidate set by walking a field's token btree in sorted
// byte order. The first distinct document it reaches gets the highest score
// (== cardinality), the next gets cardinality-1, and so on down to 1. Because
// numeric values are stored with a *sortable* byte encoding (PutSortableInteger
// / PutSortableFloat), byte order equals numeric order, so this is an ASCENDING
// numeric sort: the smallest value ranks first.
//
// Invariants these tests pin:
//   - first token  → highest score, last token → lowest score
//   - exact scores are cardinality, cardinality-1, ... down to 1
//   - ties (same token value) break by internal doc index ascending, which is
//     alphabetical-by-id ascending after BuildFrom
//   - the candidate bitmap is honored: only docs in it are scored, and
//     cardinality is the bitmap's cardinality, not the corpus size
//   - a multi-valued field ranks a doc by its SMALLEST token
//   - a candidate doc that has no token in the sort field is left unscored and
//     is therefore dropped by ResolveBM25 (it does not sort last — it vanishes)
//   - unknown field hash and empty candidate set are no-ops, never panics
// ══════════════════════════════════════════════════════════════════════════════

// Field hashes local to these tests. They must not collide with fieldBody(1),
// fieldTitle(2) or fieldNotes(3) declared in searcher_bm25_test.go.
const (
	fieldHash  = uint64(10) // numeric sort field
	fieldPrice = uint64(11) // second numeric sort field (floats)
	fieldKind  = uint64(12) // keyword field used to build candidate sets
)

// ── Core mechanism: first token wins, exact scores ───────────────────────────

// Token order, not document id order, must drive the ranking. The ids here are
// deliberately the reverse of the token order so an implementation that
// accidentally sorted by id (or insertion order) would fail.
func TestFieldScoreOrdersByTokenAscending(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("z-doc", testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("aaa", 1))),
		testsuite.MakeDoc("m-doc", testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("mmm", 1))),
		testsuite.MakeDoc("a-doc", testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("zzz", 1))),
	)

	idxs, ctx := testsuite.RunFieldScore(s, fieldKind, nil)

	// Smallest token ("aaa", held by "z-doc") ranks first even though its id
	// sorts last; largest token ("zzz", held by "a-doc") ranks last.
	assertions.Equal([]string{"z-doc", "m-doc", "a-doc"}, testsuite.ResolveDocumentIndexes(s, idxs))

	// Exact scores: cardinality, cardinality-1, ... down to 1.
	assertions.Equal(float32(3.0), scoreByID(s, ctx, "z-doc"), "first token must score == cardinality")
	assertions.Equal(float32(2.0), scoreByID(s, ctx, "m-doc"))
	assertions.Equal(float32(1.0), scoreByID(s, ctx, "a-doc"), "last token must score == 1")

	assertSortedDescByScore(assertions, ctx, idxs)
}

// ── Integer fields: ascending numeric sort across input orderings ────────────

func TestFieldScoreIntegerAscending(t *testing.T) {
	type kv struct {
		id  string
		val int64
	}

	type Test struct {
		name string
		docs []kv
		want []string // expected ranking, best (smallest value) first
	}

	tests := []Test{
		{
			name: "already ascending",
			docs: []kv{{"d-a", 10}, {"d-b", 20}, {"d-c", 30}},
			want: []string{"d-a", "d-b", "d-c"},
		},
		{
			name: "input descending, ids scrambled vs values",
			docs: []kv{{"d-x", 30}, {"d-a", 10}, {"d-z", 20}},
			want: []string{"d-a", "d-z", "d-x"}, // 10, 20, 30
		},
		{
			name: "with negatives and zero",
			docs: []kv{{"d-x", 30}, {"d-a", -10}, {"d-m", 0}, {"d-z", 20}},
			want: []string{"d-a", "d-m", "d-z", "d-x"}, // -10, 0, 20, 30
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			docs := make([]*storage.Document, 0, len(tc.docs))
			for _, d := range tc.docs {
				docs = append(docs,
					testsuite.MakeDoc(d.id,
						testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(d.val), 1)),
					),
				)
			}
			s := buildStorage(docs...)

			idxs, ctx := testsuite.RunFieldScore(s, fieldHash, nil)

			assertions.Equal(tc.want, testsuite.ResolveDocumentIndexes(s, idxs), "must be ascending by integer value")
			assertSortedDescByScore(assertions, ctx, idxs)
			// Top result carries the full cardinality as its score.
			assertions.Equal(float32(len(tc.docs)), ctx.Scoring.Get(0, idxs[0]))
		})
	}
}

// ── Float fields: ascending numeric sort, signed ─────────────────────────────

func TestFieldScoreFloatAscending(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("big", testsuite.MakeField(fieldPrice, 1, testsuite.MakeToken(testsuite.SortableFloat64(100.0), 1))),
		testsuite.MakeDoc("neg", testsuite.MakeField(fieldPrice, 1, testsuite.MakeToken(testsuite.SortableFloat64(-3.5), 1))),
		testsuite.MakeDoc("zero", testsuite.MakeField(fieldPrice, 1, testsuite.MakeToken(testsuite.SortableFloat64(0.0), 1))),
		testsuite.MakeDoc("mid", testsuite.MakeField(fieldPrice, 1, testsuite.MakeToken(testsuite.SortableFloat64(2.25), 1))),
	)

	idxs, ctx := testsuite.RunFieldScore(s, fieldPrice, nil)

	// -3.5, 0.0, 2.25, 100.0
	assertions.Equal([]string{"neg", "zero", "mid", "big"}, testsuite.ResolveDocumentIndexes(s, idxs))
	assertSortedDescByScore(assertions, ctx, idxs)
}

// ── Ties: equal values break by internal doc index (alphabetical id) ──────────

func TestFieldScoreTiesBrokenByDocIndex(t *testing.T) {
	assertions := assert.New(t)

	// "a-doc" and "b-doc" share value 10. BuildFrom assigns indices
	// alphabetically (a-doc=0, b-doc=1, c-doc=2), and within a single token's
	// posting list FieldScore walks indices ascending — so a-doc is reached
	// before b-doc and scores higher.
	s := buildStorage(
		testsuite.MakeDoc("c-doc", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(20), 1))),
		testsuite.MakeDoc("b-doc", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(10), 1))),
		testsuite.MakeDoc("a-doc", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(10), 1))),
	)

	idxs, ctx := testsuite.RunFieldScore(s, fieldHash, nil)

	assertions.Equal([]string{"b-doc", "a-doc", "c-doc"}, testsuite.ResolveDocumentIndexes(s, idxs))
	assertions.Equal(float32(3.0), scoreByID(s, ctx, "b-doc"), "document b-doc")
	assertions.Equal(float32(2.0), scoreByID(s, ctx, "a-doc"), "document a-doc")
	assertions.Equal(float32(1.0), scoreByID(s, ctx, "c-doc"), "document c-doc")
}

// ── Candidate bitmap is honored, cardinality is the bitmap's ──────────────────

func TestFieldScoreHonorsCandidateBitmap(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("p10", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(10), 1))),
		testsuite.MakeDoc("p20", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(20), 1))),
		testsuite.MakeDoc("p30", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(30), 1))),
		testsuite.MakeDoc("p40", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(40), 1))),
	)

	// Restrict the candidate set to just p10 and p30.
	i10, _ := testsuite.IndexOfDocument(s, "p10")
	i30, _ := testsuite.IndexOfDocument(s, "p30")

	idxs, ctx := testsuite.RunFieldScore(s, fieldHash, []uint32{i10, i30})

	got := testsuite.ResolveDocumentIndexes(s, idxs)
	assertions.Equal([]string{"p10", "p30"}, got, "only candidate-set docs ranked, in ascending order")

	// Cardinality is 2 (the bitmap), not 4 (the corpus): top score == 2.
	assertions.Equal(float32(2.0), scoreByID(s, ctx, "p10"))
	assertions.Equal(float32(1.0), scoreByID(s, ctx, "p30"))

	// Excluded docs never appear.
	assertions.NotContains(got, "p20")
	assertions.NotContains(got, "p40")
}

// ── Multi-valued field ranks a doc by its smallest token ──────────────────────

func TestFieldScoreMultiValuedRanksBySmallestToken(t *testing.T) {
	assertions := assert.New(t)

	// "dx" carries two tokens in the same field ("aaa" and "ccc"). It is first
	// reached at the smallest token "aaa", scored once, then skipped at "ccc".
	// "dy" carries "bbb". Sorted token walk: aaa(dx), bbb(dy), ccc(dx-skip).
	s := buildStorage(
		testsuite.MakeDoc("dx", testsuite.MakeField(fieldKind, 2,
			testsuite.MakeToken("aaa", 1), testsuite.MakeToken("ccc", 1))),
		testsuite.MakeDoc("dy", testsuite.MakeField(fieldKind, 1,
			testsuite.MakeToken("bbb", 1))),
	)

	idxs, ctx := testsuite.RunFieldScore(s, fieldKind, nil)

	assertions.Equal([]string{"dx", "dy"}, testsuite.ResolveDocumentIndexes(s, idxs),
		"dx must rank first because its smallest token sorts before dy's token")
	assertions.Equal(float32(2.0), scoreByID(s, ctx, "dx"), "dx scored once, at its smallest token")
	assertions.Equal(float32(1.0), scoreByID(s, ctx, "dy"))
}

// ── Pipeline: filter by keyword, then re-rank survivors by a numeric field ────

func TestFieldScorePipelineFilterThenSort(t *testing.T) {
	assertions := assert.New(t)

	// Each doc has a keyword "kind" field and a numeric "amount" field.
	s := buildStorage(
		testsuite.MakeDoc("c-big",
			testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("contrato", 1)),
			testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(300), 1))),
		testsuite.MakeDoc("c-small",
			testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("contrato", 1)),
			testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(100), 1))),
		testsuite.MakeDoc("c-mid",
			testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("contrato", 1)),
			testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(200), 1))),
		testsuite.MakeDoc("x-huge",
			testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("otro", 1)),
			testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(999), 1))),
	)

	searcher := query.New(s)
	ctx := &query.QueryContext{}

	// 1. Build the candidate set with a normal filter.
	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("contrato"), 1.0, 0)
	searcher.FilterDocuments(ctx, q)

	// 2. Re-rank the survivors by the numeric amount field (ascending).
	searcher.FieldScore(ctx, fieldHash)
	idxs := searcher.ResolveScores(ctx)

	assertions.Equal([]string{"c-small", "c-mid", "c-big"}, testsuite.ResolveDocumentIndexes(s, idxs),
		"survivors ranked ascending by amount")

	got := testsuite.ResolveDocumentIndexes(s, idxs)
	assertions.NotContains(got, "x-huge", "filtered-out doc must not appear despite having an amount")
}

// ── A candidate doc with no token in the sort field is dropped ────────────────

// This pins current behavior: FieldScore only scores docs it reaches through
// the field's posting lists. A candidate that has no value for the sort field
// never gets a score, so ResolveBM25 (which skips score == 0) drops it. It does
// NOT sort last — it disappears. Flagging in case "missing sorts last" is the
// behavior you actually want; if so this test is the place to change.
func TestFieldScoreDocMissingSortFieldExcluded(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("has",
			testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("contrato", 1)),
			testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(10), 1))),
		testsuite.MakeDoc("lacks",
			testsuite.MakeField(fieldKind, 1, testsuite.MakeToken("contrato", 1))),
	)

	idxs, ctx := testsuite.RunFieldScore(s, fieldHash, nil)

	got := testsuite.ResolveDocumentIndexes(s, idxs)
	assertions.Equal([]string{"has"}, got)
	assertions.NotContains(got, "lacks")
	assertions.Zero(scoreByID(s, ctx, "lacks"), "doc without the sort field stays unscored")
}

// ── No-op / edge cases: must never panic ──────────────────────────────────────

func TestFieldScoreUnknownFieldIsNoop(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("d-a", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(10), 1))),
		testsuite.MakeDoc("d-b", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(20), 1))),
	)

	assertions.NotPanics(func() {
		idxs, ctx := testsuite.RunFieldScore(s, uint64(0xDEADBEEF), nil)
		assertions.Empty(idxs, "unknown field yields no ranking")
		assertions.Nil(ctx.Scoring.Candidates, "unknown field must return before allocating scores")
	})
}

func TestFieldScoreEmptyCandidateSet(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("d-a", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(10), 1))),
		testsuite.MakeDoc("d-b", testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(20), 1))),
	)

	assertions.NotPanics(func() {
		// Non-nil empty slice → empty candidate bitmap (cardinality 0).
		idxs, ctx := testsuite.RunFieldScore(s, fieldHash, []uint32{})
		assertions.Empty(idxs)
		assertions.Empty(ctx.Scoring.Candidates)
	})
}

// ── Property: scores are exactly cardinality, cardinality-1, ..., 1 ───────────

// For a single-valued field over the whole corpus, FieldScore must hand out the
// dense, distinct, gap-free score sequence {1 .. cardinality}. This catches any
// off-by-one in the `cardinality - len(scores)` counter.
func TestFieldScoreScoreSequenceIsDense(t *testing.T) {
	assertions := assert.New(t)

	const n = 12
	docs := make([]*storage.Document, 0, n)
	for i := range n {
		// Spread values so every doc lands in its own token/posting list.
		docs = append(docs,
			testsuite.MakeDoc(
				// ids intentionally unrelated to value order
				"doc-"+string(rune('a'+i)),
				testsuite.MakeField(fieldHash, 1, testsuite.MakeToken(testsuite.SortableInt64(int64((i*7)%n)), 1)),
			),
		)
	}
	s := buildStorage(docs...)

	idxs, ctx := testsuite.RunFieldScore(s, fieldHash, nil)
	assertions.Len(idxs, n)

	got := make([]float32, 0, n)
	for _, idx := range idxs {
		got = append(got, ctx.Scoring.Get(0, idx))
	}

	want := make([]float32, 0, n)
	for s := n; s >= 1; s-- {
		want = append(want, float32(s))
	}
	assertions.Equal(want, got, "scores must be the dense descending run cardinality..1")

	// And the ranking is strictly ascending by the underlying value: rebuild the
	// expected id order independently and compare.
	type vi struct {
		id  string
		val int64
	}
	pairs := make([]vi, 0, n)
	for i := range n {
		pairs = append(pairs, vi{id: "doc-" + string(rune('a'+i)), val: int64((i * 7) % n)})
	}
	slices.SortFunc(pairs, func(a, b vi) int {
		if a.val != b.val {
			return int(a.val - b.val)
		}
		return slices.Compare([]byte(a.id), []byte(b.id))
	})
	wantIDs := make([]string, 0, n)
	for _, p := range pairs {
		wantIDs = append(wantIDs, p.id)
	}
	assertions.Equal(wantIDs, testsuite.ResolveDocumentIndexes(s, idxs))
}
