package query

import (
	"cmp"
	"slices"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/RogueTeam/textiplex/storage"
)

type SimpleQuery struct {
	Shoulds Clause
	Musts   Clause
	// Must not will not make use of boost
	MustNots Clause
}

func NewSimpleQuery() *SimpleQuery {
	return &SimpleQuery{}
}

// Query context intended to be cached and reused by caller on each search
type QueryContext struct {
	Bitmap roaring64.Bitmap
}

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (q *SimpleQuery) FilterDocuments(ctx *QueryContext, s *storage.Storage) {
	if q.Musts.Count() == 0 && q.Shoulds.Count() == 0 {
		// Invalid query inverse matching is so expensive to even attempt to have it
		return
	}

	// Process shoulds
	var firstPopulated bool
	q.Shoulds.Iter(
		ctx,
		s,
		func(state *ClauseState) {
			ctx.Bitmap.Or(&s.PostingLists[state.Token.PostingListIndex].Bitmap)
			if !firstPopulated {
				firstPopulated = true
			}
		},
	)

	// Process musts
	q.Musts.Iter(
		ctx,
		s,
		func(state *ClauseState) {
			if !firstPopulated {
				ctx.Bitmap.Or(&s.PostingLists[state.Token.PostingListIndex].Bitmap)
				firstPopulated = true
			} else {
				ctx.Bitmap.And(&s.PostingLists[state.Token.PostingListIndex].Bitmap)
			}
		},
	)

	// Process must nots
	if firstPopulated {
		q.MustNots.Iter(
			ctx,
			s,
			func(state *ClauseState) {
				ctx.Bitmap.AndNot(&s.PostingLists[state.Token.PostingListIndex].Bitmap)
			},
		)
	}
}

// Once a filtering is done scoring is the next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (q *SimpleQuery) BM25(ctx *QueryContext) (idxs []uint64) {
	type bm25 struct {
		docIdx uint64
		score  float64
	}

	scores := make([]bm25, 0, ctx.Bitmap.GetCardinality())

	// TODO: Populate scores

	slices.SortFunc(scores, func(a, b bm25) int { return cmp.Compare(a.score, b.score) })

	idxs = make([]uint64, len(scores))
	for index := range scores {
		idxs[index] = scores[index].docIdx
	}
	return idxs
}
