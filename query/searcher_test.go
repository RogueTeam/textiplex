package query_test

import (
	"fmt"
	"math"
	"math/rand"
	"slices"
	"testing"

	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

// Field hashes used across the query tests. They must match the hashes passed
// to MakeField at build time — query clauses look documents up by field hash.
const (
	fieldBody  = uint64(1)
	fieldTitle = uint64(2)
)

// runQuery filters then scores a query against s, returning the ranked doc
// indices (best first) alongside the populated context so assertions can read
// raw scores and the resolved bitmap.
func runQuery(q *query.SimpleQuery, s *storage.Storage) (idxs []uint64, ctx *query.QueryContext) {
	searcher := query.New(s)
	ctx = &query.QueryContext{}
	searcher.FilterDocuments(ctx, q)
	idxs = searcher.ResolveBM25(ctx)
	return idxs, ctx
}

// docIndexOf returns the internal index assigned to an external doc id after
// the alphabetical sort performed by SortAndBuildFrom.
func docIndexOf(s *storage.Storage, id string) (uint64, bool) {
	for i, d := range s.DocumentsIds {
		if string(d) == id {
			return uint64(i), true
		}
	}
	return 0, false
}

// resolvedDocIDs maps a ranked slice of internal indices back to external ids.
func resolvedDocIDs(s *storage.Storage, idxs []uint64) []string {
	out := make([]string, len(idxs))
	for i, idx := range idxs {
		out[i] = string(s.DocumentsIds[idx])
	}
	return out
}

// ── Single term Should: matching set and IDF/TF sanity ────────────────────────

func TestShouldSingleTerm(t *testing.T) {
	type Test struct {
		name      string
		docs      []*storage.Document
		term      string
		wantDocs  []string // expected matching set, any order
		wantEmpty bool
	}

	tests := []Test{
		{
			name: "term present in one doc",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("medellin", 1))),
			},
			term:     "contrato",
			wantDocs: []string{"doc-a"},
		},
		{
			name: "term present in several docs",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
				testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("otro", 1))),
			},
			term:     "contrato",
			wantDocs: []string{"doc-a", "doc-b"},
		},
		{
			name: "term absent from index",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
			},
			term:      "inexistente",
			wantEmpty: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var s storage.Storage
			s.SortAndBuildFrom(tc.docs...)

			q := &query.SimpleQuery{}
			q.Shoulds.Keyword([]byte(tc.term), 1.0)

			idxs, ctx := runQuery(q, &s)

			if tc.wantEmpty {
				assertions.Empty(idxs, "no document should match")
				return
			}

			got := resolvedDocIDs(&s, idxs)
			slices.Sort(got)
			want := slices.Clone(tc.wantDocs)
			slices.Sort(want)
			assertions.Equal(want, got, "matching set mismatch")

			// Every matched doc must carry a strictly positive score.
			for _, idx := range idxs {
				assertions.Greater(ctx.Scores[idx], 0.0, "doc %d must be scored", idx)
			}
		})
	}
}

// ── Ranking: higher term frequency ranks higher (same field length) ───────────

func TestRankingByTermFrequency(t *testing.T) {
	assertions := assert.New(t)

	// Both docs have identical length, so only raw TF separates them.
	// doc-hi mentions the term 5 times, doc-lo once.
	var s storage.Storage
	s.SortAndBuildFrom(
		testsuite.MakeDoc("doc-lo", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-hi", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 5))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, &s)

	want := []string{"doc-hi", "doc-lo"}
	assertions.Equal(want, resolvedDocIDs(&s, idxs), "higher TF must rank first")

	hi, _ := docIndexOf(&s, "doc-hi")
	lo, _ := docIndexOf(&s, "doc-lo")
	assertions.Greater(ctx.Scores[hi], ctx.Scores[lo])
}

// ── Ranking: shorter document ranks higher at equal term frequency ────────────

func TestRankingByDocumentLength(t *testing.T) {
	assertions := assert.New(t)

	// Same TF (1) but different field length. The shorter doc is denser in the
	// term and must score higher under BM25 length normalization.
	var s storage.Storage
	s.SortAndBuildFrom(
		testsuite.MakeDoc("doc-long", testsuite.MakeField(fieldBody, 100, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-short", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)

	idxs, _ := runQuery(q, &s)

	assertions.Equal([]string{"doc-short", "doc-long"}, resolvedDocIDs(&s, idxs),
		"shorter doc must rank first at equal TF")
}

// ── Ranking: rarer term contributes more (IDF) ────────────────────────────────

func TestRankingByInverseDocumentFrequency(t *testing.T) {
	assertions := assert.New(t)

	// "rare" appears in 1 of 4 docs, "common" in 3 of 4. A doc matched only by
	// the rare term should outscore a doc matched only by the common term, all
	// else equal (same length, same TF).
	var s storage.Storage
	s.SortAndBuildFrom(
		testsuite.MakeDoc("doc-rare", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("rare", 1))),
		testsuite.MakeDoc("doc-common", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("common", 1))),
		testsuite.MakeDoc("doc-pad1", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("common", 1))),
		testsuite.MakeDoc("doc-pad2", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("common", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("rare"), 1.0)
	q.Shoulds.Keyword([]byte("common"), 1.0)

	_, ctx := runQuery(q, &s)

	rare, _ := docIndexOf(&s, "doc-rare")
	common, _ := docIndexOf(&s, "doc-common")
	assertions.Greater(ctx.Scores[rare], ctx.Scores[common],
		"doc matched by the rarer term must score higher")
}

// ── Must: intersection semantics ──────────────────────────────────────────────

func TestMustIntersection(t *testing.T) {
	assertions := assert.New(t)

	// Only doc-ab contains BOTH terms; Must clauses must intersect.
	var s storage.Storage
	s.SortAndBuildFrom(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("solo", 1))),
		testsuite.MakeDoc("doc-ab", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("interventoria", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("interventoria", 1), testsuite.MakeToken("otro", 1))),
	)

	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("contrato"), 1.0)
	q.Musts.Keyword([]byte("interventoria"), 1.0)

	idxs, _ := runQuery(q, &s)

	assertions.Equal([]string{"doc-ab"}, resolvedDocIDs(&s, idxs),
		"only the doc containing both terms must survive intersection")
}

// ── MustNot: exclusion semantics ──────────────────────────────────────────────

func TestMustNotExclusion(t *testing.T) {
	assertions := assert.New(t)

	var s storage.Storage
	s.SortAndBuildFrom(
		testsuite.MakeDoc("doc-keep", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("limpio", 1))),
		testsuite.MakeDoc("doc-drop", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)
	q.MustNots.Keyword([]byte("vetado"), 1.0)

	idxs, ctx := runQuery(q, &s)

	assertions.Equal([]string{"doc-keep"}, resolvedDocIDs(&s, idxs),
		"doc carrying the excluded term must be removed")

	drop, _ := docIndexOf(&s, "doc-drop")
	assertions.False(ctx.Bitmap.Contains(drop), "excluded doc must not be in the resolved bitmap")
}

// ── Combined Should + Must + MustNot ──────────────────────────────────────────

func TestCombinedClauses(t *testing.T) {
	assertions := assert.New(t)

	// Must: "contrato". Should: "bogota" (boosts ranking). MustNot: "anulado".
	var s storage.Storage
	s.SortAndBuildFrom(
		// has contrato + bogota, no anulado -> matches, boosted
		testsuite.MakeDoc("doc-best", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("bogota", 1))),
		// has contrato, no bogota, no anulado -> matches, lower score
		testsuite.MakeDoc("doc-ok", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("medellin", 1))),
		// has contrato but also anulado -> excluded
		testsuite.MakeDoc("doc-banned", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("anulado", 1))),
		// no contrato -> excluded by must
		testsuite.MakeDoc("doc-irrelevant", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("bogota", 1))),
	)

	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("contrato"), 1.0)
	q.Shoulds.Keyword([]byte("bogota"), 1.0)
	q.MustNots.Keyword([]byte("anulado"), 1.0)

	idxs, _ := runQuery(q, &s)

	assertions.Equal([]string{"doc-best", "doc-ok"}, resolvedDocIDs(&s, idxs),
		"must-match minus must-not, ranked with should boost first")
}

// ── FieldKeyword: scoping a term to one field ─────────────────────────────────

func TestFieldScopedKeyword(t *testing.T) {
	assertions := assert.New(t)

	// "alerta" lives in the title of doc-title and in the body of doc-body.
	// A FieldKeyword scoped to fieldTitle must match only doc-title.
	var s storage.Storage
	s.SortAndBuildFrom(
		testsuite.MakeDoc("doc-title",
			testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("alerta", 1)),
			testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("relleno", 1)),
		),
		testsuite.MakeDoc("doc-body",
			testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("normal", 1)),
			testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("alerta", 1)),
		),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.FieldKeyword(fieldTitle, []byte("alerta"), 1.0)

	idxs, _ := runQuery(q, &s)

	assertions.Equal([]string{"doc-title"}, resolvedDocIDs(&s, idxs),
		"field-scoped keyword must ignore matches in other fields")
}

// ── Boost: scales contribution of a should term ───────────────────────────────

func TestBoostAffectsScore(t *testing.T) {
	assertions := assert.New(t)

	build := func() *storage.Storage {
		s := &storage.Storage{}
		s.SortAndBuildFrom(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("contrato", 1))),
		)
		return s
	}

	s1 := build()
	q1 := &query.SimpleQuery{}
	q1.Shoulds.Keyword([]byte("contrato"), 1.0)
	_, ctx1 := runQuery(q1, s1)
	base := ctx1.Scores[0]

	s2 := build()
	q2 := &query.SimpleQuery{}
	q2.Shoulds.Keyword([]byte("contrato"), 2.0)
	_, ctx2 := runQuery(q2, s2)
	boosted := ctx2.Scores[0]

	assertions.Greater(base, 0.0)
	assertions.InDelta(2.0*base, boosted, 1e-9, "boost of 2.0 must double the term contribution")
}

// ── Empty / degenerate queries ────────────────────────────────────────────────

func TestEmptyQuery(t *testing.T) {
	assertions := assert.New(t)

	var s storage.Storage
	s.SortAndBuildFrom(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 1, testsuite.MakeToken("contrato", 1))),
	)

	// No shoulds, no musts: filtering must short-circuit, no docs returned.
	q := &query.SimpleQuery{}
	idxs, ctx := runQuery(q, &s)

	assertions.Empty(idxs)
	assertions.Zero(ctx.Bitmap.GetCardinality())
}

// ── BM25 scoring unit checks (no index) ───────────────────────────────────────

func TestBM25Primitives(t *testing.T) {
	assertions := assert.New(t)

	t.Run("idf decreases as term gets more common", func(t *testing.T) {
		rare := query.InverseDocumentFrequency(1000, 1)
		common := query.InverseDocumentFrequency(1000, 500)
		assertions.Greater(rare, common, "rarer term must have higher idf")
		assertions.Greater(common, 0.0)
	})

	t.Run("normalized tf saturates", func(t *testing.T) {
		// As tf→∞ this formula approaches (saturation+1): the saturation*lengthNorm
		// term in the denominator becomes negligible against tf.
		ceiling := query.DefaultSaturation + 1.0 // 2.2
		hi := query.NormalizedTF(1_000_000, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.Less(hi, ceiling)
		assertions.InDelta(ceiling, hi, 0.01, "huge tf should sit just under the ceiling")
	})

	t.Run("normalized tf monotonic in tf", func(t *testing.T) {
		a := query.NormalizedTF(1, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		b := query.NormalizedTF(2, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.Greater(b, a)
	})

	t.Run("length penalty punishes long docs", func(t *testing.T) {
		short := query.NormalizedTF(1, 5, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		long := query.NormalizedTF(1, 20, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.Greater(short, long, "same tf, longer doc must score lower")
	})

	t.Run("scores are finite", func(t *testing.T) {
		score := query.ScoreTermBM25(1000, 3, 4, 12, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.False(math.IsNaN(score) || math.IsInf(score, 0))
		assertions.Greater(score, 0.0)
	})
}

// ── local helpers (names chosen to avoid clashing with the base suite) ────────

// buildStorage sorts and indexes docs, returning a ready *storage.Storage.
func buildStorage(docs ...*storage.Document) *storage.Storage {
	s := &storage.Storage{}
	s.SortAndBuildFrom(docs...)
	return s
}

// scoreByID resolves an external id to its internal index and returns its score.
// Returns NaN if the id is unknown so a misuse surfaces loudly in assertions.
func scoreByID(s *storage.Storage, ctx *query.QueryContext, id string) float64 {
	idx, ok := docIndexOf(s, id)
	if !ok {
		return math.NaN()
	}
	return ctx.Scores[idx]
}

// assertSortedDescByScore verifies ResolveBM25 returned best-first.
func assertSortedDescByScore(a *assert.Assertions, ctx *query.QueryContext, idxs []uint64) {
	for i := 1; i < len(idxs); i++ {
		a.GreaterOrEqual(ctx.Scores[idxs[i-1]], ctx.Scores[idxs[i]],
			"results must be ordered by descending score (pos %d vs %d)", i-1, i)
	}
}

// ══════════════════════════════════════════════════════════════════════════════
// SHOULD semantics
// ══════════════════════════════════════════════════════════════════════════════

// A doc matching more should terms must outscore a doc matching fewer, all else
// equal (same field length, same per-term IDF, same TF).
func TestShouldMultiTermAdditive(t *testing.T) {
	assertions := assert.New(t)

	// alpha and beta each appear in exactly 2 of 3 docs, so their IDF is equal.
	s := buildStorage(
		testsuite.MakeDoc("doc-both", testsuite.MakeField(fieldBody, 4,
			testsuite.MakeToken("alpha", 1), testsuite.MakeToken("beta", 1))),
		testsuite.MakeDoc("doc-alpha", testsuite.MakeField(fieldBody, 4,
			testsuite.MakeToken("alpha", 1), testsuite.MakeToken("relleno", 1))),
		testsuite.MakeDoc("doc-beta", testsuite.MakeField(fieldBody, 4,
			testsuite.MakeToken("beta", 1), testsuite.MakeToken("relleno", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("alpha"), 1.0)
	q.Shoulds.Keyword([]byte("beta"), 1.0)

	idxs, ctx := runQuery(q, s)

	assertions.Equal("doc-both", string(s.DocumentsIds[idxs[0]]), "two-term match must rank first")
	assertions.Greater(scoreByID(s, ctx, "doc-both"), scoreByID(s, ctx, "doc-alpha"),
		"matching both terms must score strictly higher than matching one")
}

// Adding the same keyword twice must never reduce a doc's score (it either
// double-counts or is deduped to an equal value) and must still match.
func TestShouldDuplicateTerm(t *testing.T) {
	assertions := assert.New(t)

	docs := func() *storage.Storage {
		return buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("contrato", 1))),
		)
	}

	q1 := &query.SimpleQuery{}
	q1.Shoulds.Keyword([]byte("contrato"), 1.0)
	idxs1, ctx1 := runQuery(q1, docs())

	q2 := &query.SimpleQuery{}
	q2.Shoulds.Keyword([]byte("contrato"), 1.0)
	q2.Shoulds.Keyword([]byte("contrato"), 1.0)
	idxs2, ctx2 := runQuery(q2, docs())

	assertions.Len(idxs1, 1)
	assertions.Len(idxs2, 1)
	assertions.GreaterOrEqual(ctx2.Scores[idxs2[0]], ctx1.Scores[idxs1[0]],
		"a repeated should term must not lower the score")
}

// A term present in every document still has positive IDF under the smoothed
// Lucene formula, so all docs match and all carry a positive score.
func TestShouldTermInAllDocs(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, s)

	assertions.Len(idxs, 3, "every doc must match")
	for _, idx := range idxs {
		assertions.Greater(ctx.Scores[idx], 0.0, "smoothed idf keeps the score positive even at n==N")
	}
}

// Four docs of equal length differing only in TF must rank in strict TF order.
func TestShouldRankingFullOrder(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("tf1", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("tf2", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 2))),
		testsuite.MakeDoc("tf3", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 3))),
		testsuite.MakeDoc("tf4", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 4))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, s)

	assertions.Equal([]string{"tf4", "tf3", "tf2", "tf1"}, resolvedDocIDs(s, idxs))
	assertSortedDescByScore(assertions, ctx, idxs)
}

// An unscoped Keyword must match the term in ANY field (complement to the
// field-scoped behaviour proven in the base suite).
func TestKeywordSpansFields(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-title",
			testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("alerta", 1)),
			testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("relleno", 1)),
		),
		testsuite.MakeDoc("doc-body",
			testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("normal", 1)),
			testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("alerta", 1)),
		),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("alerta"), 1.0)

	idxs, _ := runQuery(q, s)
	got := resolvedDocIDs(s, idxs)
	slices.Sort(got)

	assertions.Equal([]string{"doc-body", "doc-title"}, got,
		"unscoped keyword must match the term regardless of field")
}

// ══════════════════════════════════════════════════════════════════════════════
// MUST semantics
// ══════════════════════════════════════════════════════════════════════════════

// Three Must clauses must intersect: only the doc carrying all three survives.
func TestMustThreeWayIntersection(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-all", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1), testsuite.MakeToken("c", 1))),
		testsuite.MakeDoc("doc-ab", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1), testsuite.MakeToken("z", 1))),
		testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("c", 1), testsuite.MakeToken("y", 1), testsuite.MakeToken("x", 1))),
	)

	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("a"), 1.0)
	q.Musts.Keyword([]byte("b"), 1.0)
	q.Musts.Keyword([]byte("c"), 1.0)

	idxs, _ := runQuery(q, s)
	assertions.Equal([]string{"doc-all"}, resolvedDocIDs(s, idxs))
}

// A Must term absent from a candidate empties the intersection.
func TestMustNoMatchEmpty(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("solo", 1))),
	)

	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("contrato"), 1.0)
	q.Musts.Keyword([]byte("inexistente"), 1.0)

	idxs, ctx := runQuery(q, s)
	assertions.Empty(idxs)
	assertions.Zero(ctx.Bitmap.GetCardinality())
}

// A Must term present in every doc returns the full corpus.
func TestMustAllMatch(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
	)

	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, s)
	assertions.Len(idxs, 2)
	assertions.Equal(uint64(2), ctx.Bitmap.GetCardinality())
}

// Among Must-matched docs, a Should clause must reorder the ranking.
func TestMustPlusShouldRanking(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-boosted", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("bogota", 1))),
		testsuite.MakeDoc("doc-plain", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("medellin", 1))),
	)

	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("contrato"), 1.0)
	q.Shoulds.Keyword([]byte("bogota"), 1.0)

	idxs, _ := runQuery(q, s)
	assertions.Equal([]string{"doc-boosted", "doc-plain"}, resolvedDocIDs(s, idxs),
		"should clause must lift the doc that carries it")
}

// ══════════════════════════════════════════════════════════════════════════════
// MUSTNOT semantics
// ══════════════════════════════════════════════════════════════════════════════

// MustNot can empty the result set entirely.
func TestMustNotRemovesAll(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)
	q.MustNots.Keyword([]byte("vetado"), 1.0)

	idxs, ctx := runQuery(q, s)
	assertions.Empty(idxs)
	assertions.Zero(ctx.Bitmap.GetCardinality())
}

// A MustNot term that appears in no document is a no-op.
func TestMustNotAbsentIsNoop(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)
	q.MustNots.Keyword([]byte("jamas"), 1.0)

	idxs, _ := runQuery(q, s)
	got := resolvedDocIDs(s, idxs)
	slices.Sort(got)
	assertions.Equal([]string{"doc-a", "doc-b"}, got, "absent exclusion must keep every match")
}

// Multiple MustNot terms each independently exclude their carriers.
func TestMustNotMultiple(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-keep", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-x", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetadoX", 1))),
		testsuite.MakeDoc("doc-y", testsuite.MakeField(fieldBody, 3,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetadoY", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)
	q.MustNots.Keyword([]byte("vetadoX"), 1.0)
	q.MustNots.Keyword([]byte("vetadoY"), 1.0)

	idxs, ctx := runQuery(q, s)
	assertions.Equal([]string{"doc-keep"}, resolvedDocIDs(s, idxs))
	x, _ := docIndexOf(s, "doc-x")
	y, _ := docIndexOf(s, "doc-y")
	assertions.False(ctx.Bitmap.Contains(x))
	assertions.False(ctx.Bitmap.Contains(y))
}

// MustNot is a pure post-filter: a survivor's score is identical whether or not
// the excluded docs are present (BM25 uses corpus-wide stats, and the removed
// docs do not carry the scored term).
func TestMustNotIsPureFilter(t *testing.T) {
	assertions := assert.New(t)

	build := func() *storage.Storage {
		return buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-noise", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("vetado", 1))),
		)
	}

	base := &query.SimpleQuery{}
	base.Shoulds.Keyword([]byte("contrato"), 1.0)
	sBase := build()
	_, ctxBase := runQuery(base, sBase)

	filtered := &query.SimpleQuery{}
	filtered.Shoulds.Keyword([]byte("contrato"), 1.0)
	filtered.MustNots.Keyword([]byte("vetado"), 1.0)
	sFil := build()
	_, ctxFil := runQuery(filtered, sFil)

	assertions.InDelta(scoreByID(sBase, ctxBase, "doc-a"), scoreByID(sFil, ctxFil, "doc-a"), 1e-9,
		"excluding an unrelated doc must not change a survivor's score")
}

// ══════════════════════════════════════════════════════════════════════════════
// Field scoping
// ══════════════════════════════════════════════════════════════════════════════

func TestFieldKeywordScoping(t *testing.T) {
	build := func() *storage.Storage {
		return buildStorage(
			testsuite.MakeDoc("doc-title-hit",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("alerta", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("relleno", 1)),
			),
			testsuite.MakeDoc("doc-body-hit",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("normal", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("alerta", 1)),
			),
		)
	}

	t.Run("scoped match sets bitmap cardinality", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()
		q := &query.SimpleQuery{}
		q.Shoulds.FieldKeyword(fieldTitle, []byte("alerta"), 1.0)
		idxs, ctx := runQuery(q, s)
		assertions.Equal([]string{"doc-title-hit"}, resolvedDocIDs(s, idxs))
		assertions.Equal(uint64(1), ctx.Bitmap.GetCardinality())
	})

	t.Run("nonexistent field matches nothing", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()
		q := &query.SimpleQuery{}
		q.Shoulds.FieldKeyword(uint64(9999), []byte("alerta"), 1.0)
		idxs, ctx := runQuery(q, s)
		assertions.Empty(idxs)
		assertions.Zero(ctx.Bitmap.GetCardinality())
	})

	t.Run("scoped is stricter than unscoped", func(t *testing.T) {
		assertions := assert.New(t)

		scoped := &query.SimpleQuery{}
		scoped.Shoulds.FieldKeyword(fieldBody, []byte("alerta"), 1.0)
		sScoped := build()
		idxScoped, _ := runQuery(scoped, sScoped)

		unscoped := &query.SimpleQuery{}
		unscoped.Shoulds.Keyword([]byte("alerta"), 1.0)
		sUn := build()
		idxUn, _ := runQuery(unscoped, sUn)

		assertions.Equal([]string{"doc-body-hit"}, resolvedDocIDs(sScoped, idxScoped))
		assertions.Len(idxUn, 2, "unscoped must match both fields")
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Boost
// ══════════════════════════════════════════════════════════════════════════════

func TestBoostScalesLinearly(t *testing.T) {
	build := func() *storage.Storage {
		return buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("contrato", 1))),
		)
	}

	baseQ := &query.SimpleQuery{}
	baseQ.Shoulds.Keyword([]byte("contrato"), 1.0)
	_, baseCtx := runQuery(baseQ, build())
	base := baseCtx.Scores[0]

	cases := []struct {
		name  string
		boost float64
		want  float64
	}{
		{"half boost halves contribution", 0.5, 0.5 * base},
		{"triple boost triples contribution", 3.0, 3.0 * base},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			q := &query.SimpleQuery{}
			q.Shoulds.Keyword([]byte("contrato"), tc.boost)
			_, ctx := runQuery(q, build())
			assertions.Greater(base, 0.0)
			assertions.InDelta(tc.want, ctx.Scores[0], 1e-9)
		})
	}
}

// A large enough boost on a rarer/equal term must flip ranking order.
func TestBoostFlipsRanking(t *testing.T) {
	assertions := assert.New(t)

	// alpha and beta each appear once across two docs: equal IDF, TF and length.
	s := buildStorage(
		testsuite.MakeDoc("doc-alpha", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("alpha", 1))),
		testsuite.MakeDoc("doc-beta", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("beta", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("alpha"), 1.0)
	q.Shoulds.Keyword([]byte("beta"), 5.0)

	idxs, _ := runQuery(q, s)
	assertions.Equal([]string{"doc-beta", "doc-alpha"}, resolvedDocIDs(s, idxs),
		"the heavily boosted term must win the top slot")
}

// ══════════════════════════════════════════════════════════════════════════════
// Result/context structural invariants
// ══════════════════════════════════════════════════════════════════════════════

// Identical queries on identical data must produce identical orderings.
func TestDeterministicOrdering(t *testing.T) {
	assertions := assert.New(t)

	build := func() *storage.Storage {
		return buildStorage(
			testsuite.MakeDoc("d1", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("d2", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 2))),
			testsuite.MakeDoc("d3", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 3))),
		)
	}

	q1 := &query.SimpleQuery{}
	q1.Shoulds.Keyword([]byte("contrato"), 1.0)
	idxs1, _ := runQuery(q1, build())

	q2 := &query.SimpleQuery{}
	q2.Shoulds.Keyword([]byte("contrato"), 1.0)
	idxs2, _ := runQuery(q2, build())

	assertions.Equal(idxs1, idxs2, "ranking must be deterministic")
}

// Returned indices must be unique, all present in the bitmap, and the count must
// match the bitmap cardinality (small corpus, no truncation concerns).
func TestResultsConsistentWithBitmap(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 2))),
		testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("otro", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, s)

	assertions.Equal(uint64(len(idxs)), ctx.Bitmap.GetCardinality(),
		"every bitmap member must be returned and vice versa")

	seen := map[uint64]bool{}
	for _, idx := range idxs {
		assertions.False(seen[idx], "duplicate index %d in results", idx)
		seen[idx] = true
		assertions.True(ctx.Bitmap.Contains(idx), "returned index %d must be in the bitmap", idx)
	}
}

// Non-matched docs must be absent from the bitmap and carry a zero score.
func TestScoresZeroForNonMatched(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-match", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-miss1", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("otro", 1))),
		testsuite.MakeDoc("doc-miss2", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("otro", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, s)
	assertions.Equal([]string{"doc-match"}, resolvedDocIDs(s, idxs))

	for _, id := range []string{"doc-miss1", "doc-miss2"} {
		idx, _ := docIndexOf(s, id)
		assertions.False(ctx.Bitmap.Contains(idx), "%s must not be matched", id)
		assertions.Zero(ctx.Scores[idx], "%s must have zero score", id)
	}
}

// An empty index must not panic and must yield no results.
func TestEmptyStorageNoPanic(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage()

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, s)
	assertions.Empty(idxs)
	assertions.Zero(ctx.Bitmap.GetCardinality())
}

// A query with only MustNot clauses has nothing to match and short-circuits.
func TestMustNotOnlyIsEmpty(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
	)

	q := &query.SimpleQuery{}
	q.MustNots.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, s)
	assertions.Empty(idxs)
	assertions.Zero(ctx.Bitmap.GetCardinality())
}

// ══════════════════════════════════════════════════════════════════════════════
// BM25 primitive math (no index)
// ══════════════════════════════════════════════════════════════════════════════

func TestBM25IDFExtended(t *testing.T) {
	t.Run("strictly decreasing as the term gets more common", func(t *testing.T) {
		assertions := assert.New(t)
		a := query.InverseDocumentFrequency(1000, 1)
		b := query.InverseDocumentFrequency(1000, 10)
		c := query.InverseDocumentFrequency(1000, 100)
		d := query.InverseDocumentFrequency(1000, 500)
		e := query.InverseDocumentFrequency(1000, 900)
		assertions.Greater(a, b)
		assertions.Greater(b, c)
		assertions.Greater(c, d)
		assertions.Greater(d, e)
	})

	t.Run("always positive even when present in every doc", func(t *testing.T) {
		assertions := assert.New(t)
		all := query.InverseDocumentFrequency(1000, 1000)
		assertions.Greater(all, 0.0, "smoothed idf must stay positive at n==N")
	})

	t.Run("rarer beats omnipresent", func(t *testing.T) {
		assertions := assert.New(t)
		rare := query.InverseDocumentFrequency(1000, 1)
		all := query.InverseDocumentFrequency(1000, 1000)
		assertions.Greater(rare, all)
	})
}

func TestBM25NormalizedTFExtended(t *testing.T) {
	t.Run("zero tf yields zero", func(t *testing.T) {
		assertions := assert.New(t)
		got := query.NormalizedTF(0, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.Zero(got)
	})

	t.Run("strictly monotonic in tf", func(t *testing.T) {
		assertions := assert.New(t)
		a := query.NormalizedTF(1, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		b := query.NormalizedTF(2, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		c := query.NormalizedTF(5, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.Greater(b, a)
		assertions.Greater(c, b)
	})

	t.Run("length penalty zero removes length dependence", func(t *testing.T) {
		assertions := assert.New(t)
		short := query.NormalizedTF(3, 5, 100, query.DefaultSaturation, 0.0)
		long := query.NormalizedTF(3, 500, 100, query.DefaultSaturation, 0.0)
		assertions.InDelta(short, long, 1e-12, "with b=0 doc length must not matter")
	})

	t.Run("invariant to penalty when docLen equals avgDocLen", func(t *testing.T) {
		assertions := assert.New(t)
		// factor (1 - b + b*docLen/avgDocLen) == 1 when docLen==avgDocLen, any b.
		noPenalty := query.NormalizedTF(3, 10, 10, query.DefaultSaturation, 0.0)
		fullPenalty := query.NormalizedTF(3, 10, 10, query.DefaultSaturation, 1.0)
		assertions.InDelta(noPenalty, fullPenalty, 1e-12)
	})

	t.Run("higher saturation raises the ceiling", func(t *testing.T) {
		assertions := assert.New(t)
		low := query.NormalizedTF(1_000_000, 10, 10, 1.2, query.DefaultLengthPenalty)
		high := query.NormalizedTF(1_000_000, 10, 10, 2.0, query.DefaultLengthPenalty)
		assertions.Greater(high, low, "larger k1 must lift the saturation ceiling")
	})
}

func TestBM25ScoreExtended(t *testing.T) {
	t.Run("score equals idf times normalized tf", func(t *testing.T) {
		assertions := assert.New(t)
		idf := query.InverseDocumentFrequency(1000, 7)
		ntf := query.NormalizedTF(3, 12, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		got := query.ScoreTermBM25(1000, 7, 3, 12, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.InDelta(idf*ntf, got, 1e-9, "ScoreTermBM25 must compose idf and normalized tf")
	})

	t.Run("monotonic in tf", func(t *testing.T) {
		assertions := assert.New(t)
		a := query.ScoreTermBM25(1000, 7, 1, 12, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		b := query.ScoreTermBM25(1000, 7, 2, 12, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		c := query.ScoreTermBM25(1000, 7, 5, 12, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.Greater(b, a)
		assertions.Greater(c, b)
	})

	t.Run("rarer term scores higher at equal tf and length", func(t *testing.T) {
		assertions := assert.New(t)
		rare := query.ScoreTermBM25(1000, 1, 3, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		common := query.ScoreTermBM25(1000, 500, 3, 10, 10, query.DefaultSaturation, query.DefaultLengthPenalty)
		assertions.Greater(rare, common)
	})

	t.Run("finite and positive across a parameter spread", func(t *testing.T) {
		assertions := assert.New(t)
		scores := []float64{
			query.ScoreTermBM25(10, 1, 1, 1, 1, query.DefaultSaturation, query.DefaultLengthPenalty),
			query.ScoreTermBM25(1_000_000, 1, 50, 3, 200, query.DefaultSaturation, query.DefaultLengthPenalty),
			query.ScoreTermBM25(1_000_000, 999_999, 1, 1000, 10, query.DefaultSaturation, query.DefaultLengthPenalty),
		}
		for i, sc := range scores {
			assertions.False(math.IsNaN(sc) || math.IsInf(sc, 0), "score %d must be finite", i)
			assertions.Greater(sc, 0.0, "score %d must be positive", i)
		}
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Property / fuzz-style invariants
// ══════════════════════════════════════════════════════════════════════════════

// Across a randomized corpus, a single Should term must produce results sorted
// by descending score, with no duplicates and every result present in the bitmap.
func TestPropertyResultsSortedByScore(t *testing.T) {
	assertions := assert.New(t)
	rng := rand.New(rand.NewSource(42))

	const n = 20
	docs := make([]*storage.Document, 0, n)
	for i := range n {
		fieldLen := 5 + rng.Intn(15) // 5..19
		tf := 1 + rng.Intn(fieldLen) // 1..fieldLen
		docs = append(docs, testsuite.MakeDoc(
			fmt.Sprintf("doc-%03d", i),
			testsuite.MakeField(fieldBody, uint64(fieldLen),
				testsuite.MakeToken("contrato", uint64(tf)),
				testsuite.MakeToken(fmt.Sprintf("uniq%03d", i), 1)),
		))
	}
	s := buildStorage(docs...)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)

	idxs, ctx := runQuery(q, s)

	assertions.NotEmpty(idxs)
	assertSortedDescByScore(assertions, ctx, idxs)

	seen := map[uint64]bool{}
	for _, idx := range idxs {
		assertions.False(seen[idx], "duplicate index %d", idx)
		seen[idx] = true
		assertions.True(ctx.Bitmap.Contains(idx))
		assertions.Greater(ctx.Scores[idx], 0.0)
	}
}

// Across a randomized corpus, a Should + MustNot query must return exactly the
// docs carrying the should term and lacking the excluded term.
func TestPropertyMustNotPureFilter(t *testing.T) {
	assertions := assert.New(t)
	rng := rand.New(rand.NewSource(7))

	const n = 16
	docs := make([]*storage.Document, 0, n)
	want := make([]string, 0, n)
	for i := range n {
		id := fmt.Sprintf("doc-%03d", i)
		var doc *storage.Document
		if rng.Intn(2) == 0 { // excluded: carries the vetado term
			doc = testsuite.MakeDoc(id, testsuite.MakeField(fieldBody, 6,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1)))
		} else {
			doc = testsuite.MakeDoc(id, testsuite.MakeField(fieldBody, 6,
				testsuite.MakeToken("contrato", 1)))
			want = append(want, id)
		}
		docs = append(docs, doc)
	}
	s := buildStorage(docs...)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0)
	q.MustNots.Keyword([]byte("vetado"), 1.0)

	idxs, _ := runQuery(q, s)
	got := resolvedDocIDs(s, idxs)

	slices.Sort(got)
	slices.Sort(want)
	assertions.Equal(want, got, "result must be exactly the non-excluded should matches")
}
