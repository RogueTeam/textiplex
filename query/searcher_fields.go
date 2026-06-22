package query

import "github.com/RoaringBitmap/roaring"

func (s *Searcher) FieldScore(ctx *QueryContext, fieldHash uint64) {
	field, found := s.Storage.Fields[fieldHash]
	if !found {
		return
	}

	cardinality := ctx.Bitmap.GetCardinality()
	ctx.Scores = make(map[uint32]float64, cardinality)

	var bitmapForPostingListRetrieval roaring.Bitmap
	for tokenIdx := range field.Tokens {
		token := &field.Tokens[tokenIdx]

		s.Storage.PostingLists[token.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)
		for plIt := bitmapForPostingListRetrieval.Iterator(); plIt.HasNext(); {
			docIdx := plIt.Next()

			if !ctx.Bitmap.Contains(docIdx) {
				continue
			}

			_, found := ctx.Scores[docIdx]
			if found {
				continue
			}
			ctx.Scores[docIdx] = float64(cardinality - uint64(len(ctx.Scores)))
		}
	}
}
