package query

import "github.com/RoaringBitmap/roaring"

func (s *Searcher) FieldScore(ctx *QueryContext, fieldHash uint64) {
	field, found := s.Storage.Fields[fieldHash]
	if !found {
		return
	}

	cardinality := ctx.Bitmap.GetCardinality()
	ctx.Scores = make(map[uint32]float64, cardinality)

	var docIdxs [8]uint32

	var bitmapForPostingListRetrieval roaring.Bitmap
	for tokenIdx := range field.Tokens {
		token := &field.Tokens[tokenIdx]

		s.Storage.PostingLists[token.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)

		it := bitmapForPostingListRetrieval.ManyIterator()
		for {
			n := it.NextMany(docIdxs[:])
			for _, docIdx := range docIdxs[:n] {
				if !ctx.Bitmap.Contains(docIdx) {
					continue
				}

				_, found := ctx.Scores[docIdx]
				if found {
					continue
				}
				ctx.Scores[docIdx] = float64(cardinality - uint64(len(ctx.Scores)))
			}

			if n < len(docIdxs) {
				break
			}
		}
	}
}
