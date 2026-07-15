package query

import (
	"github.com/RoaringBitmap/roaring"
	"github.com/RogueTeam/textiplex/pool"
)

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (s *Searcher) FilterDocuments(ctx *QueryContext, q *SimpleQuery) {
	if mustsCount := q.Musts.Count(); mustsCount > 0 {
		// Musts define the candidate set: intersection of all Must posting lists.
		var retrievalBitmap roaring.Bitmap
		var failed bool
		bitmapPool := pool.New[roaring.Bitmap](mustsCount)
		var bitmaps []*roaring.Bitmap
		s.Iter(&q.Musts, func(state *ClauseState) {
			if failed {
				return
			}
			if len(state.Tokens) == 0 {
				failed = true
				bitmaps = nil
				return
			}

			bitmap := bitmapPool.Get()
			for _, token := range state.Tokens {
				s.Storage.PostingLists[token.PostingListIndex].UnsafeBitmap(&retrievalBitmap)
				bitmap.Or(&retrievalBitmap)
			}
			bitmaps = append(bitmaps, bitmap)
		})

		if !failed && len(bitmaps) > 0 {
			ctx.Bitmap = *roaring.FastAnd(bitmaps...)
		}
	} else if q.Shoulds.Count() > 0 {
		var retrievalBitmap roaring.Bitmap
		// No Musts: Shoulds define the set (union of Should posting lists).
		s.Iter(&q.Shoulds, func(state *ClauseState) {
			if len(state.Tokens) == 0 {
				return
			}

			for _, token := range state.Tokens {
				s.Storage.PostingLists[token.PostingListIndex].UnsafeBitmap(&retrievalBitmap)
				ctx.Bitmap.Or(&retrievalBitmap)
			}
		})
	}

	if q.MustNots.Count() > 0 && ctx.Bitmap.GetCardinality() > 0 {
		var retrievalBitmap roaring.Bitmap
		// MustNots subtract from whatever the set is.
		s.Iter(&q.MustNots, func(state *ClauseState) {
			if len(state.Tokens) == 0 || ctx.Bitmap.IsEmpty() {
				return
			}

			for _, token := range state.Tokens {
				s.Storage.PostingLists[token.PostingListIndex].UnsafeBitmap(&retrievalBitmap)
				ctx.Bitmap.AndNot(&retrievalBitmap)
			}
		})
	}
}
