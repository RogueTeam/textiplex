package query

import "github.com/RoaringBitmap/roaring"

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (s *Searcher) FilterDocuments(ctx *QueryContext, q *SimpleQuery) {
	mustsCount := q.Musts.Count()
	shouldsCount := q.Shoulds.Count()
	mustNotsCount := q.MustNots.Count()

	var bitmapForPostingListRetrieval roaring.Bitmap
	if mustsCount > 0 {
		// Musts define the candidate set: intersection of all Must posting lists.
		var firstMust bool
		s.Iter(&q.Musts, func(state *ClauseState) {
			if !state.Found {
				ctx.Bitmap.Clear()
				return
			}

			s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)
			if !firstMust {
				ctx.Bitmap.Or(&bitmapForPostingListRetrieval)
				firstMust = true
			} else {
				ctx.Bitmap.And(&bitmapForPostingListRetrieval)
			}
		})
	} else if shouldsCount > 0 {
		// No Musts: Shoulds define the set (union of Should posting lists).
		s.Iter(&q.Shoulds, func(state *ClauseState) {
			if !state.Found {
				return
			}

			s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)
			ctx.Bitmap.Or(&bitmapForPostingListRetrieval)
		})
	}

	if mustNotsCount > 0 {
		// MustNots subtract from whatever the set is.
		s.Iter(&q.MustNots, func(state *ClauseState) {
			if !state.Found {
				return
			}

			s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)
			ctx.Bitmap.AndNot(&bitmapForPostingListRetrieval)
		})
	}
}
