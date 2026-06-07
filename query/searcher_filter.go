package query

import (
	"cmp"
	"slices"
)

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (s *Searcher) FilterDocuments(ctx *QueryContext, q *SimpleQuery) {
	mustsCount := q.Musts.Count()
	shouldsCount := q.Shoulds.Count()
	mustNotsCount := q.MustNots.Count()

	if mustsCount > 0 {
		// Musts define the candidate set: intersection of all Must posting lists.
		var firstMust bool
		s.Iter(&q.Musts, func(state *ClauseState) {
			if !state.Found {
				ctx.Bitmap.Clear()
				return
			}

			pl := &s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap
			if !firstMust {
				ctx.Bitmap.Or(pl)
				firstMust = true
			} else {
				ctx.Bitmap.And(pl)
			}
		})
	} else if shouldsCount > 0 {
		// No Musts: Shoulds define the set (union of Should posting lists).
		s.Iter(&q.Shoulds, func(state *ClauseState) {
			if !state.Found {
				return
			}

			ctx.Bitmap.Or(&s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap)
		})
	}

	if mustNotsCount > 0 {
		// MustNots subtract from whatever the set is.
		s.Iter(&q.MustNots, func(state *ClauseState) {
			if !state.Found {
				return
			}
			ctx.Bitmap.AndNot(&s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap)
		})
	}

	ctx.Scores = make(map[uint64]float64, ctx.Bitmap.GetCardinality())

	if mustsCount > 0 {
		s.Iter(&q.Musts, func(state *ClauseState) { s.UpdateScores(ctx, state) })
	}
	if shouldsCount > 0 {
		s.Iter(&q.Shoulds, func(state *ClauseState) { s.UpdateScores(ctx, state) })
	}
}

// Once a filtering is done scoring is the next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (s *Searcher) ResolveBM25(ctx *QueryContext) (idxs []uint64) {
	type bm25 struct {
		docIdx uint64
		score  float64
	}

	scores := make([]bm25, 0, ctx.Bitmap.GetCardinality())

	it := ctx.Bitmap.Iterator()
	for it.HasNext() {
		doxIdx := it.Next()

		score := ctx.Scores[doxIdx]
		if score == 0 {
			continue
		}

		scores = append(scores, bm25{
			score:  score,
			docIdx: doxIdx,
		})
	}

	slices.SortFunc(
		scores,
		func(a, b bm25) int {
			scoreCmp := cmp.Compare(b.score, a.score)
			if scoreCmp == 0 {
				return cmp.Compare(b.docIdx, a.docIdx)
			}
			return scoreCmp
		},
	)

	idxs = make([]uint64, len(scores))
	for index := range scores {
		idxs[index] = scores[index].docIdx
	}
	return idxs
}
