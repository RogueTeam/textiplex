package query

import (
	"github.com/RoaringBitmap/roaring"
)

func (s *Searcher) FieldScore(ctx *QueryContext, fieldHash uint64) {
	field, found := s.Storage.Fields[fieldHash]
	if !found {
		return
	}

	cardinality := ctx.Bitmap.GetCardinality()
	ctx.Scores = make(map[uint32]float64, cardinality)

	var pending = ctx.Bitmap.Clone()

	var retrievalBitmap roaring.Bitmap
	for tokenIdx := range field.Tokens {
		if pending.IsEmpty() {
			break
		}

		token := &field.Tokens[tokenIdx]

		s.Storage.PostingLists[token.PostingListIndex].UnsafeBitmap(&retrievalBitmap)

		for _, docIdx := range roaring.FastAnd(pending, &retrievalBitmap).ToArray() {
			_, found := ctx.Scores[docIdx]
			if !found {
				ctx.Scores[docIdx] = float64(cardinality - uint64(len(ctx.Scores)))
			}
		}

		pending.Xor(&retrievalBitmap)
	}
}
