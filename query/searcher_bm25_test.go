package query_test

import (
	"fmt"
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
			s.BuildFrom(tc.docs...)

			q := &query.SimpleQuery{}
			q.Shoulds.Keyword([]byte(tc.term), 1.0, 0)

			idxs, ctx := testsuite.RunQuery(q, &s)

			if tc.wantEmpty {
				assertions.Empty(idxs, "no document should match")
				return
			}

			got := testsuite.ResolveDocumentIndexes(&s, idxs)
			slices.Sort(got)
			want := slices.Clone(tc.wantDocs)
			slices.Sort(want)
			assertions.Equal(want, got, "matching set mismatch")

			// Every matched doc must carry a strictly positive score.
			for _, idx := range idxs {
				assertions.Greater(ctx.Scores[idx], float32(0.0), "doc %d must be scored", idx)
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
	s.BuildFrom(
		testsuite.MakeDoc("doc-lo", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-hi", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 5))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, &s)

	want := []string{"doc-hi", "doc-lo"}
	assertions.Equal(want, testsuite.ResolveDocumentIndexes(&s, idxs), "higher TF must rank first")

	hi, _ := testsuite.IndexOfDocument(&s, "doc-hi")
	lo, _ := testsuite.IndexOfDocument(&s, "doc-lo")
	assertions.Greater(ctx.Scores[hi], ctx.Scores[lo])
}

// ── Ranking: shorter document ranks higher at equal term frequency ────────────

func TestRankingByDocumentLength(t *testing.T) {
	assertions := assert.New(t)

	// Same TF (1) but different field length. The shorter doc is denser in the
	// term and must score higher under BM25 length normalization.
	var s storage.Storage
	s.BuildFrom(
		testsuite.MakeDoc("doc-long", testsuite.MakeField(fieldBody, 100, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-short", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, &s)

	assertions.Equal([]string{"doc-short", "doc-long"}, testsuite.ResolveDocumentIndexes(&s, idxs),
		"shorter doc must rank first at equal TF")
}

// ── Ranking: rarer term contributes more (IDF) ────────────────────────────────

func TestRankingByInverseDocumentFrequency(t *testing.T) {
	assertions := assert.New(t)

	// "rare" appears in 1 of 4 docs, "common" in 3 of 4. A doc matched only by
	// the rare term should outscore a doc matched only by the common term, all
	// else equal (same length, same TF).
	var s storage.Storage
	s.BuildFrom(
		testsuite.MakeDoc("doc-rare", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("rare", 1))),
		testsuite.MakeDoc("doc-common", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("common", 1))),
		testsuite.MakeDoc("doc-pad1", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("common", 1))),
		testsuite.MakeDoc("doc-pad2", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("common", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("rare"), 1.0, 0)
	q.Shoulds.Keyword([]byte("common"), 1.0, 0)

	_, ctx := testsuite.RunQuery(q, &s)

	rare, _ := testsuite.IndexOfDocument(&s, "doc-rare")
	common, _ := testsuite.IndexOfDocument(&s, "doc-common")
	assertions.Greater(ctx.Scores[rare], ctx.Scores[common],
		"doc matched by the rarer term must score higher")
}

// ── Must: intersection semantics ──────────────────────────────────────────────

func TestMustIntersection(t *testing.T) {
	assertions := assert.New(t)

	// Only doc-ab contains BOTH terms; Must clauses must intersect.
	var s storage.Storage
	s.BuildFrom(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("solo", 1))),
		testsuite.MakeDoc("doc-ab", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("interventoria", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("interventoria", 1), testsuite.MakeToken("otro", 1))),
	)

	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("contrato"), 1.0, 0)
	q.Musts.Keyword([]byte("interventoria"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, &s)

	assertions.Equal([]string{"doc-ab"}, testsuite.ResolveDocumentIndexes(&s, idxs),
		"only the doc containing both terms must survive intersection")
}

// ── MustNot: exclusion semantics ──────────────────────────────────────────────

func TestMustNotExclusion(t *testing.T) {
	assertions := assert.New(t)

	var s storage.Storage
	s.BuildFrom(
		testsuite.MakeDoc("doc-keep", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("limpio", 1))),
		testsuite.MakeDoc("doc-drop", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
	)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	q.MustNots.Keyword([]byte("vetado"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, &s)

	assertions.Equal([]string{"doc-keep"}, testsuite.ResolveDocumentIndexes(&s, idxs),
		"doc carrying the excluded term must be removed")

	drop, _ := testsuite.IndexOfDocument(&s, "doc-drop")
	assertions.False(ctx.Bitmap.Contains(drop), "excluded doc must not be in the resolved bitmap")
}

// ── Combined Should + Must + MustNot ──────────────────────────────────────────

func TestCombinedClauses(t *testing.T) {
	assertions := assert.New(t)

	// Must: "contrato". Should: "bogota" (boosts ranking). MustNot: "anulado".
	var s storage.Storage
	s.BuildFrom(
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
	q.Musts.Keyword([]byte("contrato"), 1.0, 0)
	q.Shoulds.Keyword([]byte("bogota"), 1.0, 0)
	q.MustNots.Keyword([]byte("anulado"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, &s)

	assertions.Equal([]string{"doc-best", "doc-ok"}, testsuite.ResolveDocumentIndexes(&s, idxs),
		"must-match minus must-not, ranked with should boost first")
}

// ── FieldKeyword: scoping a term to one field ─────────────────────────────────

func TestFieldScopedKeyword(t *testing.T) {
	assertions := assert.New(t)

	// "alerta" lives in the title of doc-title and in the body of doc-body.
	// A FieldKeyword scoped to fieldTitle must match only doc-title.
	var s storage.Storage
	s.BuildFrom(
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
	q.Shoulds.FieldKeyword(fieldTitle, []byte("alerta"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, &s)

	assertions.Equal([]string{"doc-title"}, testsuite.ResolveDocumentIndexes(&s, idxs),
		"field-scoped keyword must ignore matches in other fields")
}

// ── Boost: scales contribution of a should term ───────────────────────────────

func TestBoostAffectsScore(t *testing.T) {
	assertions := assert.New(t)

	build := func() *storage.Storage {
		s := &storage.Storage{}
		s.BuildFrom(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("contrato", 1))),
		)
		return s
	}

	s1 := build()
	q1 := &query.SimpleQuery{}
	q1.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	_, ctx1 := testsuite.RunQuery(q1, s1)
	base := ctx1.Scores[0]

	s2 := build()
	q2 := &query.SimpleQuery{}
	q2.Shoulds.Keyword([]byte("contrato"), 2.0, 0)
	_, ctx2 := testsuite.RunQuery(q2, s2)
	boosted := ctx2.Scores[0]

	assertions.Greater(base, float32(0.0))
	assertions.InDelta(2.0*base, boosted, 1e-9, "boost of 2.0 must double the term contribution")
}

// ── Empty / degenerate queries ────────────────────────────────────────────────

func TestEmptyQuery(t *testing.T) {
	assertions := assert.New(t)

	var s storage.Storage
	s.BuildFrom(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 1, testsuite.MakeToken("contrato", 1))),
	)

	// No shoulds, no musts: filtering must short-circuit, no docs returned.
	q := &query.SimpleQuery{}
	idxs, ctx := testsuite.RunQuery(q, &s)

	assertions.Empty(idxs)
	assertions.Zero(ctx.Bitmap.GetCardinality())
}

// ── local helpers (names chosen to avoid clashing with the base suite) ────────

// buildStorage sorts and indexes docs, returning a ready *storage.Storage.
func buildStorage(docs ...*storage.Document) *storage.Storage {
	s := &storage.Storage{}
	s.BuildFrom(docs...)
	return s
}

// scoreByID resolves an external id to its internal index and returns its score.
// Returns NaN if the id is unknown so a misuse surfaces loudly in assertions.
func scoreByID(s *storage.Storage, ctx *query.QueryContext, id string) float32 {
	idx, ok := testsuite.IndexOfDocument(s, id)
	if !ok {
		return 0
	}
	return ctx.Scores[idx]
}

// assertSortedDescByScore verifies ResolveBM25 returned best-first.
func assertSortedDescByScore(a *assert.Assertions, ctx *query.QueryContext, idxs []uint32) {
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
	q.Shoulds.Keyword([]byte("alpha"), 1.0, 0)
	q.Shoulds.Keyword([]byte("beta"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)

	assertions.Equal("doc-both", s.DocumentsIds[idxs[0]].Value.UnsafeString(), "two-term match must rank first")
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
	q1.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	idxs1, ctx1 := testsuite.RunQuery(q1, docs())

	q2 := &query.SimpleQuery{}
	q2.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	q2.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	idxs2, ctx2 := testsuite.RunQuery(q2, docs())

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
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)

	assertions.Len(idxs, 3, "every doc must match")
	for _, idx := range idxs {
		assertions.Greater(ctx.Scores[idx], float32(0.0), "smoothed idf keeps the score positive even at n==N")
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
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)

	assertions.Equal([]string{"tf4", "tf3", "tf2", "tf1"}, testsuite.ResolveDocumentIndexes(s, idxs))
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
	q.Shoulds.Keyword([]byte("alerta"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, s)
	got := testsuite.ResolveDocumentIndexes(s, idxs)
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
	q.Musts.Keyword([]byte("a"), 1.0, 0)
	q.Musts.Keyword([]byte("b"), 1.0, 0)
	q.Musts.Keyword([]byte("c"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, s)
	assertions.Equal([]string{"doc-all"}, testsuite.ResolveDocumentIndexes(s, idxs))
}

// A Must term absent from a candidate empties the intersection.
func TestMustNoMatchEmpty(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2,
			testsuite.MakeToken("contrato", 1), testsuite.MakeToken("solo", 1))),
	)

	q := &query.SimpleQuery{}
	q.Musts.Keyword([]byte("contrato"), 1.0, 0)
	q.Musts.Keyword([]byte("inexistente"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)
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
	q.Musts.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)
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
	q.Musts.Keyword([]byte("contrato"), 1.0, 0)
	q.Shoulds.Keyword([]byte("bogota"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, s)
	assertions.Equal([]string{"doc-boosted", "doc-plain"}, testsuite.ResolveDocumentIndexes(s, idxs),
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
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	q.MustNots.Keyword([]byte("vetado"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)
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
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	q.MustNots.Keyword([]byte("jamas"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, s)
	got := testsuite.ResolveDocumentIndexes(s, idxs)
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
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	q.MustNots.Keyword([]byte("vetadoX"), 1.0, 0)
	q.MustNots.Keyword([]byte("vetadoY"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)
	assertions.Equal([]string{"doc-keep"}, testsuite.ResolveDocumentIndexes(s, idxs))
	x, _ := testsuite.IndexOfDocument(s, "doc-x")
	y, _ := testsuite.IndexOfDocument(s, "doc-y")
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
	base.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	sBase := build()
	_, ctxBase := testsuite.RunQuery(base, sBase)

	filtered := &query.SimpleQuery{}
	filtered.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	filtered.MustNots.Keyword([]byte("vetado"), 1.0, 0)
	sFil := build()
	_, ctxFil := testsuite.RunQuery(filtered, sFil)

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
		q.Shoulds.FieldKeyword(fieldTitle, []byte("alerta"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-title-hit"}, testsuite.ResolveDocumentIndexes(s, idxs))
		assertions.Equal(uint64(1), ctx.Bitmap.GetCardinality())
	})

	t.Run("nonexistent field matches nothing", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()
		q := &query.SimpleQuery{}
		q.Shoulds.FieldKeyword(uint64(9999), []byte("alerta"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Empty(idxs)
		assertions.Zero(ctx.Bitmap.GetCardinality())
	})

	t.Run("scoped is stricter than unscoped", func(t *testing.T) {
		assertions := assert.New(t)

		scoped := &query.SimpleQuery{}
		scoped.Shoulds.FieldKeyword(fieldBody, []byte("alerta"), 1.0, 0)
		sScoped := build()
		idxScoped, _ := testsuite.RunQuery(scoped, sScoped)

		unscoped := &query.SimpleQuery{}
		unscoped.Shoulds.Keyword([]byte("alerta"), 1.0, 0)
		sUn := build()
		idxUn, _ := testsuite.RunQuery(unscoped, sUn)

		assertions.Equal([]string{"doc-body-hit"}, testsuite.ResolveDocumentIndexes(sScoped, idxScoped))
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
	baseQ.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	_, baseCtx := testsuite.RunQuery(baseQ, build())
	base := baseCtx.Scores[0]

	cases := []struct {
		name  string
		boost float32
		want  float32
	}{
		{"half boost halves contribution", 0.5, 0.5 * base},
		{"triple boost triples contribution", 3.0, 3.0 * base},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			q := &query.SimpleQuery{}
			q.Shoulds.Keyword([]byte("contrato"), tc.boost, 0)
			_, ctx := testsuite.RunQuery(q, build())
			assertions.Greater(base, float32(0.0))
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
	q.Shoulds.Keyword([]byte("alpha"), 1.0, 0)
	q.Shoulds.Keyword([]byte("beta"), 5.0, 0)

	idxs, _ := testsuite.RunQuery(q, s)
	assertions.Equal([]string{"doc-beta", "doc-alpha"}, testsuite.ResolveDocumentIndexes(s, idxs),
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
	q1.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	idxs1, _ := testsuite.RunQuery(q1, build())

	q2 := &query.SimpleQuery{}
	q2.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	idxs2, _ := testsuite.RunQuery(q2, build())

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
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)

	assertions.Equal(uint64(len(idxs)), ctx.Bitmap.GetCardinality(),
		"every bitmap member must be returned and vice versa")

	seen := map[uint32]bool{}
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
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)
	assertions.Equal([]string{"doc-match"}, testsuite.ResolveDocumentIndexes(s, idxs))

	for _, id := range []string{"doc-miss1", "doc-miss2"} {
		idx, _ := testsuite.IndexOfDocument(s, id)
		assertions.False(ctx.Bitmap.Contains(idx), "%s must not be matched", id)
		assertions.Zero(ctx.Scores[idx], "%s must have zero score", id)
	}
}

// An empty index must not panic and must yield no results.
func TestEmptyStorageNoPanic(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage()

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)
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
	q.MustNots.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)
	assertions.Empty(idxs)
	assertions.Zero(ctx.Bitmap.GetCardinality())
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
			testsuite.MakeField(fieldBody, uint32(fieldLen),
				testsuite.MakeToken("contrato", uint32(tf)),
				testsuite.MakeToken(fmt.Sprintf("uniq%03d", i), 1)),
		))
	}
	s := buildStorage(docs...)

	q := &query.SimpleQuery{}
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)

	idxs, ctx := testsuite.RunQuery(q, s)

	assertions.NotEmpty(idxs)
	assertSortedDescByScore(assertions, ctx, idxs)

	seen := map[uint32]bool{}
	for _, idx := range idxs {
		assertions.False(seen[idx], "duplicate index %d", idx)
		seen[idx] = true
		assertions.True(ctx.Bitmap.Contains(idx))
		assertions.Greater(ctx.Scores[idx], float32(float32(0.0)))
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
	q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
	q.MustNots.Keyword([]byte("vetado"), 1.0, 0)

	idxs, _ := testsuite.RunQuery(q, s)
	got := testsuite.ResolveDocumentIndexes(s, idxs)

	slices.Sort(got)
	slices.Sort(want)
	assertions.Equal(want, got, "result must be exactly the non-excluded should matches")
}

// A third field hash so multi-field behaviour can be exercised beyond the
// body/title pair the base suite uses.
const fieldNotes = uint64(3)

// resolvedIDSet maps a ranked slice of internal indices to a set of external
// ids, handy for subset / membership assertions where order is irrelevant.
func resolvedIDSet(s *storage.Storage, idxs []uint32) map[string]bool {
	out := make(map[string]bool, len(idxs))
	for _, idx := range idxs {
		out[s.DocumentsIds[idx].Value.UnsafeString()] = true
	}
	return out
}

// ══════════════════════════════════════════════════════════════════════════════
// Multi-field documents
// ══════════════════════════════════════════════════════════════════════════════

func TestMultiFieldScoring(t *testing.T) {
	t.Run("unscoped keyword matches term in any of three fields", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-title",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("alerta", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("relleno", 1)),
				testsuite.MakeField(fieldNotes, 2, testsuite.MakeToken("nota", 1)),
			),
			testsuite.MakeDoc("doc-body",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("normal", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("alerta", 1)),
				testsuite.MakeField(fieldNotes, 2, testsuite.MakeToken("nota", 1)),
			),
			testsuite.MakeDoc("doc-notes",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("normal", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("relleno", 1)),
				testsuite.MakeField(fieldNotes, 2, testsuite.MakeToken("alerta", 1)),
			),
		)

		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("alerta"), 1.0, 0)

		idxs, _ := testsuite.RunQuery(q, s)
		got := testsuite.ResolveDocumentIndexes(s, idxs)
		slices.Sort(got)
		assertions.Equal([]string{"doc-body", "doc-notes", "doc-title"}, got,
			"unscoped keyword must reach the term in every field")
	})

	t.Run("field keyword isolates the right field among three", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-title",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("alerta", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("relleno", 1)),
				testsuite.MakeField(fieldNotes, 2, testsuite.MakeToken("nota", 1)),
			),
			testsuite.MakeDoc("doc-notes",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("normal", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("relleno", 1)),
				testsuite.MakeField(fieldNotes, 2, testsuite.MakeToken("alerta", 1)),
			),
		)

		q := &query.SimpleQuery{}
		q.Shoulds.FieldKeyword(fieldNotes, []byte("alerta"), 1.0, 0)

		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-notes"}, testsuite.ResolveDocumentIndexes(s, idxs),
			"scoping to notes must skip the same term in the title field")
	})

	t.Run("shorter body field ranks higher at equal tf", func(t *testing.T) {
		assertions := assert.New(t)
		// Identical title, differing only by body length. The denser body must win.
		s := buildStorage(
			testsuite.MakeDoc("doc-long",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("cabecera", 1)),
				testsuite.MakeField(fieldBody, 100, testsuite.MakeToken("contrato", 1)),
			),
			testsuite.MakeDoc("doc-short",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("cabecera", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1)),
			),
		)

		q := &query.SimpleQuery{}
		q.Shoulds.FieldKeyword(fieldBody, []byte("contrato"), 1.0, 0)

		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-short", "doc-long"}, testsuite.ResolveDocumentIndexes(s, idxs),
			"per-field length normalization must favour the shorter body")
	})

	t.Run("term living only in notes still scores positive", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("titulo", 1)),
				testsuite.MakeField(fieldNotes, 3, testsuite.MakeToken("anexo", 1)),
			),
		)

		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("anexo"), 1.0, 0)

		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-a"}, testsuite.ResolveDocumentIndexes(s, idxs))
		assertions.Greater(ctx.Scores[idxs[0]], float32(0.0))
	})

	t.Run("two docs same term different fields both match unscoped", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-1", testsuite.MakeField(fieldTitle, 2, testsuite.MakeToken("clave", 1))),
			testsuite.MakeDoc("doc-2", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("clave", 1))),
		)

		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("clave"), 1.0, 0)

		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Len(idxs, 2)
		for _, idx := range idxs {
			assertions.Greater(ctx.Scores[idx], float32(0.0))
		}
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Boost edge cases
// ══════════════════════════════════════════════════════════════════════════════

func TestBoostEdgeCases(t *testing.T) {
	single := func() *storage.Storage {
		return buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("contrato", 1))),
		)
	}

	t.Run("zero boost contributes nothing", func(t *testing.T) {
		assertions := assert.New(t)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), float32(0.0), 0)
		_, ctx := testsuite.RunQuery(q, single())
		// The clause matched but its contribution is scaled to zero.
		assertions.InDelta(float32(0.0), ctx.Scores[0], 1e-12, "a zero boost must add no score")
	})

	t.Run("boost on must term raises score", func(t *testing.T) {
		assertions := assert.New(t)

		baseQ := &query.SimpleQuery{}
		baseQ.Musts.Keyword([]byte("contrato"), 1.0, 0)
		_, baseCtx := testsuite.RunQuery(baseQ, single())

		boostQ := &query.SimpleQuery{}
		boostQ.Musts.Keyword([]byte("contrato"), 4.0, 0)
		_, boostCtx := testsuite.RunQuery(boostQ, single())

		assertions.Greater(baseCtx.Scores[0], float32(0.0))
		assertions.InDelta(4.0*baseCtx.Scores[0], boostCtx.Scores[0], 1e-9,
			"must clauses are scored, so their boost scales linearly too")
	})

	t.Run("boost on field keyword scales score", func(t *testing.T) {
		assertions := assert.New(t)
		build := func() *storage.Storage {
			return buildStorage(
				testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldTitle, 3, testsuite.MakeToken("alerta", 1))),
			)
		}
		baseQ := &query.SimpleQuery{}
		baseQ.Shoulds.FieldKeyword(fieldTitle, []byte("alerta"), 1.0, 0)
		_, baseCtx := testsuite.RunQuery(baseQ, build())

		boostQ := &query.SimpleQuery{}
		boostQ.Shoulds.FieldKeyword(fieldTitle, []byte("alerta"), 2.5, 0)
		_, boostCtx := testsuite.RunQuery(boostQ, build())

		assertions.InDelta(2.5*baseCtx.Scores[0], boostCtx.Scores[0], 1e-9)
	})

	t.Run("boost does not change the matching set", func(t *testing.T) {
		assertions := assert.New(t)
		build := func() *storage.Storage {
			return buildStorage(
				testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("contrato", 1))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("contrato", 1))),
				testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("otro", 1))),
			)
		}
		low := &query.SimpleQuery{}
		low.Shoulds.Keyword([]byte("contrato"), 0.1, 0)
		idxLow, _ := testsuite.RunQuery(low, build())

		high := &query.SimpleQuery{}
		high.Shoulds.Keyword([]byte("contrato"), 9.0, 0)
		idxHigh, _ := testsuite.RunQuery(high, build())

		assertions.Equal(resolvedIDSet(build(), idxLow), resolvedIDSet(build(), idxHigh),
			"boost only affects ranking, never membership")
	})

	t.Run("huge boost keeps score finite", func(t *testing.T) {
		assertions := assert.New(t)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1e9, 0)
		_, ctx := testsuite.RunQuery(q, single())
		assertions.Greater(ctx.Scores[0], float32(0.0))
	})

	t.Run("boost on must-not has no scoring effect", func(t *testing.T) {
		assertions := assert.New(t)
		build := func() *storage.Storage {
			return buildStorage(
				testsuite.MakeDoc("doc-keep", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
				testsuite.MakeDoc("doc-drop", testsuite.MakeField(fieldBody, 3,
					testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
			)
		}
		q1 := &query.SimpleQuery{}
		q1.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		q1.MustNots.Keyword([]byte("vetado"), 1.0, 0)
		s1 := build()
		_, ctx1 := testsuite.RunQuery(q1, s1)

		q2 := &query.SimpleQuery{}
		q2.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		q2.MustNots.Keyword([]byte("vetado"), 50.0, 0)
		s2 := build()
		_, ctx2 := testsuite.RunQuery(q2, s2)

		assertions.InDelta(scoreByID(s1, ctx1, "doc-keep"), scoreByID(s2, ctx2, "doc-keep"), 1e-12,
			"the boost attached to an exclusion clause must be ignored")
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Clause contradictions and interactions
// ══════════════════════════════════════════════════════════════════════════════

func TestClauseContradictions(t *testing.T) {
	t.Run("same term must and must-not yields empty", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		q.MustNots.Keyword([]byte("contrato"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Empty(idxs)
		assertions.Zero(ctx.Bitmap.GetCardinality())
	})

	t.Run("same term sole should and must-not yields empty", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		q.MustNots.Keyword([]byte("contrato"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Empty(idxs)
	})

	t.Run("term in must and should matches at least must-only", func(t *testing.T) {
		assertions := assert.New(t)
		build := func() *storage.Storage {
			return buildStorage(
				testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("contrato", 1))),
			)
		}
		mustOnly := &query.SimpleQuery{}
		mustOnly.Musts.Keyword([]byte("contrato"), 1.0, 0)
		_, ctxMust := testsuite.RunQuery(mustOnly, build())

		both := &query.SimpleQuery{}
		both.Musts.Keyword([]byte("contrato"), 1.0, 0)
		both.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxBoth, ctxBoth := testsuite.RunQuery(both, build())

		assertions.Len(idxBoth, 1)
		assertions.GreaterOrEqual(ctxBoth.Scores[0], ctxMust.Scores[0],
			"repeating the term as a should must not lower the score")
	})

	t.Run("must plus must-not removes only the carriers", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-keep", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-drop", testsuite.MakeField(fieldBody, 3,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		q.MustNots.Keyword([]byte("vetado"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-keep"}, testsuite.ResolveDocumentIndexes(s, idxs))
	})

	t.Run("must-not shared by every must match empties result", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 2,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		q.MustNots.Keyword([]byte("vetado"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Empty(idxs)
		assertions.Zero(ctx.Bitmap.GetCardinality())
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Extended clause combinations
// ══════════════════════════════════════════════════════════════════════════════

func TestClauseCombinationsExtended(t *testing.T) {
	t.Run("must narrows should reranks must-not filters", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-best", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("bogota", 1))),
			testsuite.MakeDoc("doc-ok", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("cali", 1))),
			testsuite.MakeDoc("doc-banned", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("bogota", 1),
				testsuite.MakeToken("anulado", 1))),
			testsuite.MakeDoc("doc-out", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("bogota", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		q.Shoulds.Keyword([]byte("bogota"), 1.0, 0)
		q.MustNots.Keyword([]byte("anulado"), 1.0, 0)

		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-best", "doc-ok"}, testsuite.ResolveDocumentIndexes(s, idxs))
	})

	t.Run("should term absent from corpus is a no-op booster", func(t *testing.T) {
		assertions := assert.New(t)
		build := func() *storage.Storage {
			return buildStorage(
				testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 2))),
			)
		}
		plain := &query.SimpleQuery{}
		plain.Musts.Keyword([]byte("contrato"), 1.0, 0)
		idxPlain, _ := testsuite.RunQuery(plain, build())

		withGhost := &query.SimpleQuery{}
		withGhost.Musts.Keyword([]byte("contrato"), 1.0, 0)
		withGhost.Shoulds.Keyword([]byte("inexistente"), 5.0, 0)
		idxGhost, _ := testsuite.RunQuery(withGhost, build())

		assertions.Equal(testsuite.ResolveDocumentIndexes(build(), idxPlain), testsuite.ResolveDocumentIndexes(build(), idxGhost),
			"a should clause matching nothing must not perturb ranking")
	})

	t.Run("two musts plus should ranking", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-boosted", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1), testsuite.MakeToken("plus", 1))),
			testsuite.MakeDoc("doc-plain", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("a"), 1.0, 0)
		q.Musts.Keyword([]byte("b"), 1.0, 0)
		q.Shoulds.Keyword([]byte("plus"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-boosted", "doc-plain"}, testsuite.ResolveDocumentIndexes(s, idxs))
	})

	t.Run("should lifts only the subset of must matches that carry it", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-x", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-y", testsuite.MakeField(fieldBody, 5,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("destacado", 1))),
			testsuite.MakeDoc("doc-z", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		q.Shoulds.Keyword([]byte("destacado"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal("doc-y", s.DocumentsIds[idxs[0]].Value.UnsafeString(),
			"the only must-match carrying the should term must rank first")
		assertions.Len(idxs, 3)
	})

	t.Run("must-not wins over a heavy should boost", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("bogota", 1),
				testsuite.MakeToken("anulado", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("bogota", 1))),
			testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		q.Shoulds.Keyword([]byte("bogota"), 100.0, 0)
		q.MustNots.Keyword([]byte("anulado"), 1.0, 0)

		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-b", "doc-c"}, testsuite.ResolveDocumentIndexes(s, idxs))
		a, _ := testsuite.IndexOfDocument(s, "doc-a")
		assertions.False(ctx.Bitmap.Contains(a), "exclusion must hold even against a huge boost")
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// FieldKeyword variants
// ══════════════════════════════════════════════════════════════════════════════

func TestFieldKeywordVariants(t *testing.T) {
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

	t.Run("field keyword in must clause", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()
		q := &query.SimpleQuery{}
		q.Musts.FieldKeyword(fieldTitle, []byte("alerta"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-title-hit"}, testsuite.ResolveDocumentIndexes(s, idxs))
	})

	t.Run("field keyword in must-not clause excludes by field", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			// alerta lives in the body, so a title-scoped exclusion must keep it.
			testsuite.MakeDoc("doc-keep",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("normal", 1)),
				testsuite.MakeField(fieldBody, 2,
					testsuite.MakeToken("relleno", 1), testsuite.MakeToken("alerta", 1)),
			),
			// alerta in the title triggers the title-scoped exclusion.
			testsuite.MakeDoc("doc-drop",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("alerta", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("relleno", 1)),
			),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("relleno"), 1.0, 0)
		q.MustNots.FieldKeyword(fieldTitle, []byte("alerta"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-keep"}, testsuite.ResolveDocumentIndexes(s, idxs),
			"a field-scoped exclusion must not fire on the term in another field")
	})

	t.Run("two scoped musts on different fields intersect", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-both",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("alpha", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("beta", 1)),
			),
			testsuite.MakeDoc("doc-swapped",
				testsuite.MakeField(fieldTitle, 1, testsuite.MakeToken("beta", 1)),
				testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("alpha", 1)),
			),
		)
		q := &query.SimpleQuery{}
		q.Musts.FieldKeyword(fieldTitle, []byte("alpha"), 1.0, 0)
		q.Musts.FieldKeyword(fieldBody, []byte("beta"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-both"}, testsuite.ResolveDocumentIndexes(s, idxs),
			"only the doc with the right term in the right field survives")
	})

	t.Run("scoped term present only in another field matches nothing", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()
		q := &query.SimpleQuery{}
		// alerta exists, but never in the notes field.
		q.Shoulds.FieldKeyword(fieldNotes, []byte("alerta"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Empty(idxs)
		assertions.Zero(ctx.Bitmap.GetCardinality())
	})

	t.Run("scoped keyword combined with unscoped keyword unions", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()
		q := &query.SimpleQuery{}
		q.Shoulds.FieldKeyword(fieldTitle, []byte("alerta"), 1.0, 0) // hits doc-title-hit
		q.Shoulds.Keyword([]byte("relleno"), 1.0, 0)                 // hits doc-title-hit body
		idxs, _ := testsuite.RunQuery(q, s)
		got := resolvedIDSet(s, idxs)
		assertions.True(got["doc-title-hit"], "doc matched by both should clauses must be present")
	})

	t.Run("scoped keyword for an absent term in a valid field matches nothing", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()
		q := &query.SimpleQuery{}
		q.Shoulds.FieldKeyword(fieldBody, []byte("inexistente"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Empty(idxs)
		assertions.Zero(ctx.Bitmap.GetCardinality())
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Ranking subtleties
// ══════════════════════════════════════════════════════════════════════════════

func TestRankingSubtleties(t *testing.T) {
	t.Run("equal score docs are both returned", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 2))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 2))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Len(idxs, 2)
		assertions.InDelta(ctx.Scores[idxs[0]], ctx.Scores[idxs[1]], 1e-12,
			"identical docs must score identically")
	})

	t.Run("equal tf, length decides via body normalization", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-dense", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-sparse", testsuite.MakeField(fieldBody, 30, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-dense", "doc-sparse"}, testsuite.ResolveDocumentIndexes(s, idxs))
	})

	t.Run("mixed tf and length resolve to a strict order", func(t *testing.T) {
		assertions := assert.New(t)
		// doc-hi: high tf, short. doc-mid: high tf, long. doc-lo: low tf, short.
		s := buildStorage(
			testsuite.MakeDoc("doc-hi", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 4))),
			testsuite.MakeDoc("doc-mid", testsuite.MakeField(fieldBody, 50, testsuite.MakeToken("contrato", 4))),
			testsuite.MakeDoc("doc-lo", testsuite.MakeField(fieldBody, 5, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertSortedDescByScore(assertions, ctx, idxs)
		assertions.Equal("doc-hi", s.DocumentsIds[idxs[0]].Value.UnsafeString())
	})

	t.Run("one saturated tf still ranks first", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-huge", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 10000))),
			testsuite.MakeDoc("doc-one", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-huge", "doc-one"}, testsuite.ResolveDocumentIndexes(s, idxs))
		for _, idx := range idxs {
			assertions.Greater(ctx.Scores[idx], float32(0), "saturated tf must keep the score greater than zero")
		}
	})

	t.Run("single match among many misses", func(t *testing.T) {
		assertions := assert.New(t)
		docs := []*storage.Document{
			testsuite.MakeDoc("doc-hit", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
		}
		for i := range 30 {
			docs = append(docs, testsuite.MakeDoc(
				fmt.Sprintf("doc-miss-%03d", i),
				testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("ruido", 1))))
		}
		s := buildStorage(docs...)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-hit"}, testsuite.ResolveDocumentIndexes(s, idxs))
		assertions.Equal(uint64(1), ctx.Bitmap.GetCardinality())
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Corpus-level IDF behaviour through the query path
// ══════════════════════════════════════════════════════════════════════════════

func TestCorpusLevelIDF(t *testing.T) {
	t.Run("single doc corpus scores positive", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Len(idxs, 1)
		assertions.Greater(ctx.Scores[0], float32(0.0), "smoothed idf must stay positive even at N==n==1")
	})

	t.Run("rare term dominates ranking in a large corpus", func(t *testing.T) {
		assertions := assert.New(t)
		docs := []*storage.Document{
			testsuite.MakeDoc("doc-rare", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("comun", 1), testsuite.MakeToken("raro", 1))),
		}
		for i := range 20 {
			docs = append(docs, testsuite.MakeDoc(
				fmt.Sprintf("doc-comun-%03d", i),
				testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("comun", 1))))
		}
		s := buildStorage(docs...)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("comun"), 1.0, 0)
		q.Shoulds.Keyword([]byte("raro"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal("doc-rare", s.DocumentsIds[idxs[0]].Value.UnsafeString(),
			"the doc carrying the rare term must rank first")
	})

	t.Run("growing the corpus shifts scores but preserves order", func(t *testing.T) {
		assertions := assert.New(t)
		core := func() []*storage.Document {
			return []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 3))),
			}
		}
		q := func() *query.SimpleQuery {
			x := &query.SimpleQuery{}
			x.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
			return x
		}

		small := buildStorage(core()...)
		idxSmall, _ := testsuite.RunQuery(q(), small)

		bigDocs := core()
		for i := range 15 {
			bigDocs = append(bigDocs, testsuite.MakeDoc(
				fmt.Sprintf("doc-pad-%03d", i),
				testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("ruido", 1))))
		}
		big := buildStorage(bigDocs...)
		idxBig, _ := testsuite.RunQuery(q(), big)

		assertions.Equal(testsuite.ResolveDocumentIndexes(small, idxSmall), testsuite.ResolveDocumentIndexes(big, idxBig),
			"adding non-matching docs must not reorder the matches")
	})

	t.Run("terms with equal doc frequency contribute equal idf", func(t *testing.T) {
		assertions := assert.New(t)
		// alpha and beta each appear in exactly one of two equally sized docs.
		s := buildStorage(
			testsuite.MakeDoc("doc-alpha", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("alpha", 1))),
			testsuite.MakeDoc("doc-beta", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("beta", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("alpha"), 1.0, 0)
		q.Shoulds.Keyword([]byte("beta"), 1.0, 0)
		_, ctx := testsuite.RunQuery(q, s)
		assertions.InDelta(scoreByID(s, ctx, "doc-alpha"), scoreByID(s, ctx, "doc-beta"), 1e-12,
			"equal frequency, tf and length must produce equal scores")
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Degenerate and robustness inputs
// ══════════════════════════════════════════════════════════════════════════════

func TestDegenerateInputs(t *testing.T) {
	t.Run("empty keyword bytes match nothing", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte(""), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Empty(idxs, "an empty term is not a stored token")
	})

	t.Run("should term absent from the corpus yields empty", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("jamas"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Empty(idxs)
		assertions.Zero(ctx.Bitmap.GetCardinality())
	})

	t.Run("repeated identical must term matches normally", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("otro", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-a"}, testsuite.ResolveDocumentIndexes(s, idxs),
			"a doubled must term must behave like the single term")
	})

	t.Run("token filling its whole field stays finite and positive", func(t *testing.T) {
		assertions := assert.New(t)
		// Field length equals tf: the field is entirely the term (max density).
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 7, testsuite.MakeToken("contrato", 7))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Len(idxs, 1)
		assertions.Greater(ctx.Scores[0], float32(0.0))
	})

	t.Run("several must-not clauses with nothing else is empty", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.MustNots.Keyword([]byte("uno"), 1.0, 0)
		q.MustNots.Keyword([]byte("dos"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Empty(idxs)
		assertions.Zero(ctx.Bitmap.GetCardinality())
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Unicode / non-ASCII tokens
// ══════════════════════════════════════════════════════════════════════════════

func TestUnicodeTokens(t *testing.T) {
	t.Run("accented spanish term matches exactly", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-acc", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contratación", 1))),
			testsuite.MakeDoc("doc-plain", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contratacion", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contratación"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-acc"}, testsuite.ResolveDocumentIndexes(s, idxs),
			"the accented term must not collide with its unaccented form")
	})

	t.Run("enye token is distinct from its ascii lookalike", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-enye", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("niño", 1))),
			testsuite.MakeDoc("doc-nn", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("nino", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("niño"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-enye"}, testsuite.ResolveDocumentIndexes(s, idxs))
	})

	t.Run("case is significant at the query layer", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-lower", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-upper", testsuite.MakeField(fieldBody, 2, testsuite.MakeToken("Contrato", 1))),
		)
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("Contrato"), 1.0, 0)
		idxs, _ := testsuite.RunQuery(q, s)
		assertions.Equal([]string{"doc-upper"}, testsuite.ResolveDocumentIndexes(s, idxs),
			"raw byte tokens are not folded, so case must separate them")
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Structural invariants (extended)
// ══════════════════════════════════════════════════════════════════════════════

func TestStructuralInvariantsExtended(t *testing.T) {
	corpus := func() *storage.Storage {
		return buildStorage(
			testsuite.MakeDoc("d1", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("d2", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 2))),
			testsuite.MakeDoc("d3", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 3))),
		)
	}

	t.Run("scores are identical across repeated queries", func(t *testing.T) {
		assertions := assert.New(t)
		q1 := &query.SimpleQuery{}
		q1.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		s1 := corpus()
		idx1, ctx1 := testsuite.RunQuery(q1, s1)

		q2 := &query.SimpleQuery{}
		q2.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		s2 := corpus()
		idx2, ctx2 := testsuite.RunQuery(q2, s2)

		assertions.Equal(idx1, idx2)
		for _, id := range []string{"d1", "d2", "d3"} {
			assertions.InDelta(scoreByID(s1, ctx1, id), scoreByID(s2, ctx2, id), 1e-12)
		}
	})

	t.Run("should clause insertion order does not matter", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("alpha", 1), testsuite.MakeToken("beta", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("alpha", 1))),
		)
		ab := &query.SimpleQuery{}
		ab.Shoulds.Keyword([]byte("alpha"), 1.0, 0)
		ab.Shoulds.Keyword([]byte("beta"), 1.0, 0)
		sAB := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 4,
				testsuite.MakeToken("alpha", 1), testsuite.MakeToken("beta", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 4, testsuite.MakeToken("alpha", 1))),
		)
		idxAB, ctxAB := testsuite.RunQuery(ab, sAB)

		ba := &query.SimpleQuery{}
		ba.Shoulds.Keyword([]byte("beta"), 1.0, 0)
		ba.Shoulds.Keyword([]byte("alpha"), 1.0, 0)
		idxBA, ctxBA := testsuite.RunQuery(ba, s)

		assertions.Equal(testsuite.ResolveDocumentIndexes(sAB, idxAB), testsuite.ResolveDocumentIndexes(s, idxBA))
		assertions.InDelta(scoreByID(sAB, ctxAB, "doc-a"), scoreByID(s, ctxBA, "doc-a"), 1e-12)
	})

	t.Run("doc insertion order does not matter", func(t *testing.T) {
		assertions := assert.New(t)
		forward := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 2))),
			testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 3))),
		)
		reverse := buildStorage(
			testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 3))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 2))),
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 10, testsuite.MakeToken("contrato", 1))),
		)
		mk := func() *query.SimpleQuery {
			q := &query.SimpleQuery{}
			q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
			return q
		}
		idxF, ctxF := testsuite.RunQuery(mk(), forward)
		idxR, ctxR := testsuite.RunQuery(mk(), reverse)

		assertions.Equal(testsuite.ResolveDocumentIndexes(forward, idxF), testsuite.ResolveDocumentIndexes(reverse, idxR),
			"BuildFrom must canonicalize input order")
		for _, id := range []string{"doc-a", "doc-b", "doc-c"} {
			assertions.InDelta(scoreByID(forward, ctxF, id), scoreByID(reverse, ctxR, id), 1e-12)
		}
	})

	t.Run("bitmap cardinality equals result count under must", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldBody, 3,
				testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1))),
			testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldBody, 3,
				testsuite.MakeToken("a", 1), testsuite.MakeToken("b", 1))),
			testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("a", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("a"), 1.0, 0)
		q.Musts.Keyword([]byte("b"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		assertions.Equal(uint64(len(idxs)), ctx.Bitmap.GetCardinality())
	})

	t.Run("non matched docs carry zero score under combined clauses", func(t *testing.T) {
		assertions := assert.New(t)
		s := buildStorage(
			testsuite.MakeDoc("doc-keep", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("contrato", 1))),
			testsuite.MakeDoc("doc-drop", testsuite.MakeField(fieldBody, 3,
				testsuite.MakeToken("contrato", 1), testsuite.MakeToken("vetado", 1))),
			testsuite.MakeDoc("doc-miss", testsuite.MakeField(fieldBody, 3, testsuite.MakeToken("otro", 1))),
		)
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("contrato"), 1.0, 0)
		q.MustNots.Keyword([]byte("vetado"), 1.0, 0)
		_, ctx := testsuite.RunQuery(q, s)
		for _, id := range []string{"doc-drop", "doc-miss"} {
			idx, _ := testsuite.IndexOfDocument(s, id)
			assertions.False(ctx.Bitmap.Contains(idx))
			assertions.Zero(ctx.Scores[idx])
		}
	})

	t.Run("results stay unique and inside the bitmap under boost", func(t *testing.T) {
		assertions := assert.New(t)
		s := corpus()
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 3.7, 0)
		idxs, ctx := testsuite.RunQuery(q, s)
		seen := map[uint32]bool{}
		for _, idx := range idxs {
			assertions.False(seen[idx], "duplicate index %d", idx)
			seen[idx] = true
			assertions.True(ctx.Bitmap.Contains(idx))
		}
		assertions.Equal(uint64(len(idxs)), ctx.Bitmap.GetCardinality())
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Property / fuzz-style invariants (extended)
// ══════════════════════════════════════════════════════════════════════════════

func TestPropertyInvariantsExtended(t *testing.T) {
	t.Run("must of two terms is a subset of should of one", func(t *testing.T) {
		assertions := assert.New(t)
		rng := rand.New(rand.NewSource(101))
		const n = 24
		docs := make([]*storage.Document, 0, n)
		for i := range n {
			tokens := []*storage.TokenDefinition{testsuite.MakeToken("alpha", 1)}
			if rng.Intn(2) == 0 {
				tokens = append(tokens, testsuite.MakeToken("beta", 1))
			}
			docs = append(docs, testsuite.MakeDoc(
				fmt.Sprintf("doc-%03d", i),
				testsuite.MakeField(fieldBody, 6, tokens...)))
		}
		s := buildStorage(docs...)

		mustQ := &query.SimpleQuery{}
		mustQ.Musts.Keyword([]byte("alpha"), 1.0, 0)
		mustQ.Musts.Keyword([]byte("beta"), 1.0, 0)
		idxMust, _ := testsuite.RunQuery(mustQ, s)

		shouldQ := &query.SimpleQuery{}
		shouldQ.Shoulds.Keyword([]byte("alpha"), 1.0, 0)
		idxShould, _ := testsuite.RunQuery(shouldQ, s)

		shouldSet := resolvedIDSet(s, idxShould)
		for id := range resolvedIDSet(s, idxMust) {
			assertions.True(shouldSet[id], "must-result doc %s must appear in the broader should-result", id)
		}
	})

	t.Run("adding a must-not never grows the result", func(t *testing.T) {
		assertions := assert.New(t)
		rng := rand.New(rand.NewSource(202))
		const n = 20
		docs := make([]*storage.Document, 0, n)
		for i := range n {
			tokens := []*storage.TokenDefinition{testsuite.MakeToken("alpha", 1)}
			if rng.Intn(3) == 0 {
				tokens = append(tokens, testsuite.MakeToken("beta", 1))
			}
			docs = append(docs, testsuite.MakeDoc(
				fmt.Sprintf("doc-%03d", i),
				testsuite.MakeField(fieldBody, 6, tokens...)))
		}
		s := buildStorage(docs...)

		base := &query.SimpleQuery{}
		base.Shoulds.Keyword([]byte("alpha"), 1.0, 0)
		idxBase, _ := testsuite.RunQuery(base, s)

		filtered := &query.SimpleQuery{}
		filtered.Shoulds.Keyword([]byte("alpha"), 1.0, 0)
		filtered.MustNots.Keyword([]byte("beta"), 1.0, 0)
		idxFiltered, _ := testsuite.RunQuery(filtered, s)

		assertions.LessOrEqual(len(idxFiltered), len(idxBase),
			"an exclusion clause can only shrink or preserve the set")
	})

	t.Run("boost preserves the matching set", func(t *testing.T) {
		assertions := assert.New(t)
		rng := rand.New(rand.NewSource(303))
		const n = 18
		mk := func() *storage.Storage {
			r := rand.New(rand.NewSource(303))
			docs := make([]*storage.Document, 0, n)
			for i := range n {
				tf := uint32(1 + r.Intn(4))
				docs = append(docs, testsuite.MakeDoc(
					fmt.Sprintf("doc-%03d", i),
					testsuite.MakeField(fieldBody, 8, testsuite.MakeToken("contrato", tf))))
			}
			return buildStorage(docs...)
		}
		_ = rng

		low := &query.SimpleQuery{}
		low.Shoulds.Keyword([]byte("contrato"), 0.3, 0)
		sLow := mk()
		idxLow, _ := testsuite.RunQuery(low, sLow)

		high := &query.SimpleQuery{}
		high.Shoulds.Keyword([]byte("contrato"), 12.0, 0)
		sHigh := mk()
		idxHigh, _ := testsuite.RunQuery(high, sHigh)

		assertions.Equal(resolvedIDSet(sLow, idxLow), resolvedIDSet(sHigh, idxHigh))
	})

	t.Run("all scores positive and finite on a random multi-field corpus", func(t *testing.T) {
		assertions := assert.New(t)
		rng := rand.New(rand.NewSource(404))
		const n = 25
		docs := make([]*storage.Document, 0, n)
		for i := range n {
			titleLen := uint32(1 + rng.Intn(3))
			bodyLen := uint32(5 + rng.Intn(20))
			tf := uint32(1 + rng.Intn(int(bodyLen)))
			docs = append(docs, testsuite.MakeDoc(
				fmt.Sprintf("doc-%03d", i),
				testsuite.MakeField(fieldTitle, titleLen, testsuite.MakeToken("titulo", 1)),
				testsuite.MakeField(fieldBody, bodyLen,
					testsuite.MakeToken("contrato", tf),
					testsuite.MakeToken(fmt.Sprintf("uniq%03d", i), 1)),
			))
		}
		s := buildStorage(docs...)

		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
		idxs, ctx := testsuite.RunQuery(q, s)

		assertions.NotEmpty(idxs)
		assertSortedDescByScore(assertions, ctx, idxs)
		for _, idx := range idxs {
			sc := ctx.Scores[idx]
			assertions.Greater(sc, float32(0.0))
		}
	})

	t.Run("doc insertion order is invariant on a random corpus", func(t *testing.T) {
		assertions := assert.New(t)
		rng := rand.New(rand.NewSource(505))
		const n = 22
		base := make([]*storage.Document, 0, n)
		for i := range n {
			tf := uint32(1 + rng.Intn(5))
			base = append(base, testsuite.MakeDoc(
				fmt.Sprintf("doc-%03d", i),
				testsuite.MakeField(fieldBody, 12, testsuite.MakeToken("contrato", tf))))
		}
		reversed := slices.Clone(base)
		slices.Reverse(reversed)

		mk := func() *query.SimpleQuery {
			q := &query.SimpleQuery{}
			q.Shoulds.Keyword([]byte("contrato"), 1.0, 0)
			return q
		}
		sForward := buildStorage(base...)
		idxF, ctxF := testsuite.RunQuery(mk(), sForward)

		sReverse := buildStorage(reversed...)
		idxR, ctxR := testsuite.RunQuery(mk(), sReverse)

		assertions.Equal(testsuite.ResolveDocumentIndexes(sForward, idxF), testsuite.ResolveDocumentIndexes(sReverse, idxR))
		for i := range idxF {
			idF, idR := sForward.DocumentsIds[idxF[i]].Value.UnsafeString(), sReverse.DocumentsIds[idxR[i]].Value.UnsafeString()
			assertions.Equal(idF, idR)
			assertions.InDelta(ctxF.Scores[idxF[i]], ctxR.Scores[idxR[i]], 1e-12)
		}
	})
}
