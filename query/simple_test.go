package query_test

import (
	"math"
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
	ctx = &query.QueryContext{}
	q.FilterDocuments(ctx, s)
	idxs = q.BM25(ctx)
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

			q := query.NewSimpleQuery()
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

	q := query.NewSimpleQuery()
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

	q := query.NewSimpleQuery()
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

	q := query.NewSimpleQuery()
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

	q := query.NewSimpleQuery()
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

	q := query.NewSimpleQuery()
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

	q := query.NewSimpleQuery()
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

	q := query.NewSimpleQuery()
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
	q1 := query.NewSimpleQuery()
	q1.Shoulds.Keyword([]byte("contrato"), 1.0)
	_, ctx1 := runQuery(q1, s1)
	base := ctx1.Scores[0]

	s2 := build()
	q2 := query.NewSimpleQuery()
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
	q := query.NewSimpleQuery()
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
