package query

import (
	"github.com/RoaringBitmap/roaring"
)

func (s *Searcher) FieldScore(ctx *QueryContext, fieldHash uint64) {
	field, found := s.Storage.Fields[fieldHash]
	if !found {
		return
	}

	ctx.Scoring.Reset(&ctx.Bitmap)

	var pending = ctx.Bitmap.Clone()

	var assigned uint32
	var retrievalBitmap roaring.Bitmap
	for tokenIdx := range field.Tokens {
		if pending.IsEmpty() {
			break
		}

		token := &field.Tokens[tokenIdx]

		s.Storage.PostingLists[token.PostingListIndex].UnsafeBitmap(&retrievalBitmap)

		resolved := roaring.FastAnd(pending, &retrievalBitmap)
		if resolved.IsEmpty() {
			continue
		}

		resolvedArray := resolved.ToArray()
		for _, docIdx := range resolvedArray {
			score := float32(uint32(ctx.Scoring.Len()) - assigned)
			assigned++
			ctx.Scoring.Add(docIdx, score)
		}

		pending.AndNot(&retrievalBitmap)
	}
}
