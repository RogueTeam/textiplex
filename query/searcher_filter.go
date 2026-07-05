package query

import (
	"github.com/RoaringBitmap/roaring"
)

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (s *Searcher) FilterDocuments(ctx *QueryContext, q *SimpleQuery) {
	var retrievalBitmap roaring.Bitmap
	if q.Musts.Count() > 0 {
		// Musts define the candidate set: intersection of all Must posting lists.
		var firstMust bool
		s.Iter(&q.Musts, func(state *ClauseState) {
			if !state.Found {
				ctx.Bitmap.Clear()
				return
			}

			s.Storage.PostingLists[state.Token.PostingListIndex].UnsafeBitmap(&retrievalBitmap)
			if !firstMust {
				ctx.Bitmap.Or(&retrievalBitmap)
				firstMust = true
			} else {
				ctx.Bitmap.And(&retrievalBitmap)
			}
		})
	} else if q.Shoulds.Count() > 0 {
		// No Musts: Shoulds define the set (union of Should posting lists).
		s.Iter(&q.Shoulds, func(state *ClauseState) {
			if !state.Found {
				return
			}

			s.Storage.PostingLists[state.Token.PostingListIndex].UnsafeBitmap(&retrievalBitmap)
			ctx.Bitmap.Or(&retrievalBitmap)
		})
	}

	if q.MustNots.Count() > 0 {
		// MustNots subtract from whatever the set is.
		s.Iter(&q.MustNots, func(state *ClauseState) {
			if !state.Found {
				return
			}

			s.Storage.PostingLists[state.Token.PostingListIndex].UnsafeBitmap(&retrievalBitmap)
			ctx.Bitmap.AndNot(&retrievalBitmap)
		})
	}
}
