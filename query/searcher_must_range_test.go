package query_test

import (
	"testing"

	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

// ══════════════════════════════════════════════════════════════════════════════
// Must-range retrieval
//
// A range Must (e.g. +price:>0) expands to one clause entry spanning many
// numeric tokens. FilterDocuments must UNION those tokens (they are one
// conjunct) and only then intersect against other entries. The pre-fix code
// did an And per token, so it required a single document to carry every
// distinct value in the range at once and always returned the empty set. It
// also skipped the first yielded token by position, which dropped a legitimate
// match whenever the bound value was not itself an indexed token.
// ══════════════════════════════════════════════════════════════════════════════

const (
	fieldAmount = uint64(20) // numeric range field (int64, sortable)
	fieldKindMR = uint64(21) // keyword field for mixed keyword+range queries
)

type amountDoc struct {
	id    string
	value int64
}

// buildAmounts indexes one doc per (id, value) into fieldAmount, each value a
// distinct number so every value gets its own posting list — the exact shape
// that made the per-token And collapse to empty.
func buildAmounts(pairs ...amountDoc) *storage.Storage {
	docs := make([]*storage.Document, 0, len(pairs))
	for _, p := range pairs {
		docs = append(docs,
			testsuite.MakeDoc(p.id,
				testsuite.MakeField(fieldAmount, 1, testsuite.MakeToken(testsuite.SortableInt64(p.value), 1)),
			),
		)
	}
	s := &storage.Storage{}
	s.BuildFrom(docs...)
	return s
}

func ids(s *storage.Storage, idxs []uint32) []string {
	return testsuite.ResolveDocumentIndexes(s, idxs)
}

// Core repro: +amount:>0 over docs whose values are all distinct. Pre-fix this
// returned nothing (the And-per-token wipe). Post-fix it returns every doc with
// a value strictly greater than zero, and excludes the zero doc itself.
func TestMustRangeGreaterExcludesLowReturnsRest(t *testing.T) {
	a := assert.New(t)

	s := buildAmounts(
		amountDoc{"d-neg", -5},
		amountDoc{"d-zero", 0},
		amountDoc{"d-a", 10},
		amountDoc{"d-b", 20},
		amountDoc{"d-c", 30},
	)

	q := &query.SimpleQuery{}
	// +amount:>0  → RangeCaptureModeRight, low=0, high=open
	q.Musts.FieldRange(fieldAmount, testsuite.SortableInt64(0), nil, query.RangeCaptureModeRight, 1.0)

	idxs, _ := testsuite.RunQuery(q, s)
	got := ids(s, idxs)

	a.ElementsMatch([]string{"d-a", "d-b", "d-c"}, got,
		"a Must range must union its tokens, not intersect them")
	a.NotContains(got, "d-zero", "strictly-greater must exclude the low bound")
	a.NotContains(got, "d-neg")
}

// >=0 includes the boundary value.
func TestMustRangeGreaterEqualIncludesLow(t *testing.T) {
	a := assert.New(t)

	s := buildAmounts(
		amountDoc{"d-neg", -5},
		amountDoc{"d-zero", 0},
		amountDoc{"d-a", 10},
	)

	q := &query.SimpleQuery{}
	// +amount:>=0 → RangeCaptureModeBoth, low=0, high=open
	q.Musts.FieldRange(fieldAmount, testsuite.SortableInt64(0), nil, query.RangeCaptureModeBoth, 1.0)

	idxs, _ := testsuite.RunQuery(q, s)
	a.ElementsMatch([]string{"d-zero", "d-a"}, ids(s, idxs))
}

// Isolates the positional first-token-skip bug WITHOUT the And-wipe: the range
// matches exactly one token, and the bound (25) is not an indexed value. The
// old code skipped the first yielded token (30) by position and returned empty;
// the fix skips by value, so 30 is kept.
func TestMustRangeGreaterUnindexedBoundSingleMatch(t *testing.T) {
	a := assert.New(t)

	s := buildAmounts(
		amountDoc{"d-a", 10},
		amountDoc{"d-b", 20},
		amountDoc{"d-c", 30},
	)

	q := &query.SimpleQuery{}
	// +amount:>25 (25 not indexed) → only 30 qualifies
	q.Musts.FieldRange(fieldAmount, testsuite.SortableInt64(25), nil, query.RangeCaptureModeRight, 1.0)

	idxs, _ := testsuite.RunQuery(q, s)
	a.Equal([]string{"d-c"}, ids(s, idxs),
		"the first in-range token must not be dropped when the bound value is absent")
}

// <20 excludes the high bound; open lower scans from the smallest value.
func TestMustRangeLessExcludesHigh(t *testing.T) {
	a := assert.New(t)

	s := buildAmounts(
		amountDoc{"d-neg", -5},
		amountDoc{"d-a", 10},
		amountDoc{"d-b", 20},
		amountDoc{"d-c", 30},
	)

	q := &query.SimpleQuery{}
	// +amount:<20 → RangeCaptureModeLeft, low=open, high=20
	q.Musts.FieldRange(fieldAmount, nil, testsuite.SortableInt64(20), query.RangeCaptureModeLeft, 1.0)

	idxs, _ := testsuite.RunQuery(q, s)
	got := ids(s, idxs)
	a.ElementsMatch([]string{"d-neg", "d-a"}, got)
	a.NotContains(got, "d-b", "strictly-less must exclude the high bound")
}

// A must range absent from every document empties the result — and does not
// resurrect via the old clear-then-Or path.
func TestMustRangeNoMatchEmpty(t *testing.T) {
	a := assert.New(t)

	s := buildAmounts(
		amountDoc{"d-a", 10},
		amountDoc{"d-b", 20},
	)

	q := &query.SimpleQuery{}
	// +amount:>1000 → nothing qualifies
	q.Musts.FieldRange(fieldAmount, testsuite.SortableInt64(1000), nil, query.RangeCaptureModeRight, 1.0)

	idxs, _ := testsuite.RunQuery(q, s)
	a.Empty(ids(s, idxs))
}

// A keyword Must intersected with a range Must: two separate entries, so the
// range union is intersected against the keyword posting list. The range spans
// several positive values held by keyword-matching docs, so the old per-token
// And genuinely collapses this (not just the union-vs-intersect coincidence of
// a single-token range).
func TestMustKeywordIntersectMustRange(t *testing.T) {
	a := assert.New(t)

	// Each doc carries a "kind" keyword and an "amount" numeric value.
	mk := func(id, kind string, amount int64) *storage.Document {
		return testsuite.MakeDoc(id,
			testsuite.MakeField(fieldKindMR, 1, testsuite.MakeToken(kind, 1)),
			testsuite.MakeField(fieldAmount, 1, testsuite.MakeToken(testsuite.SortableInt64(amount), 1)),
		)
	}
	s := &storage.Storage{}
	s.BuildFrom(
		mk("d-c-10", "contract", 10),  // contract ∧ >0  → match
		mk("d-c-20", "contract", 20),  // contract ∧ >0  → match
		mk("d-c-0", "contract", 0),    // contract but not >0
		mk("d-inv-30", "invoice", 30), // >0 but not contract
	)

	q := &query.SimpleQuery{}
	q.Musts.FieldKeyword(fieldKindMR, []byte("contract"), 1.0, 0)
	q.Musts.FieldRange(fieldAmount, testsuite.SortableInt64(0), nil, query.RangeCaptureModeRight, 1.0)

	idxs, _ := testsuite.RunQuery(q, s)
	a.ElementsMatch([]string{"d-c-10", "d-c-20"}, ids(s, idxs),
		"result must be (kind==contract) ∩ (amount>0)")
}

// A pure must-range query must survive scoring: BM25 has to leave the retrieved
// docs with a positive score, otherwise ResolveScores drops them and the fix to
// FilterDocuments would be invisible end to end.
func TestMustRangeSurvivesScoring(t *testing.T) {
	a := assert.New(t)

	s := buildAmounts(
		amountDoc{"d-a", 10},
		amountDoc{"d-b", 20},
		amountDoc{"d-c", 30},
	)

	q := &query.SimpleQuery{}
	q.Musts.FieldRange(fieldAmount, testsuite.SortableInt64(0), nil, query.RangeCaptureModeRight, 1.0)

	idxs, ctx := testsuite.RunQuery(q, s)
	a.Len(idxs, 3)
	for _, idx := range idxs {
		a.Positive(ctx.Scoring.Get(0, idx), "every retrieved doc must carry a positive score")
	}
}
