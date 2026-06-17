package query

func (s *Searcher) FieldScore(ctx *QueryContext, fieldHash uint64) {
	field, found := s.Storage.Fields[fieldHash]
	if !found {
		return
	}

	cardinality := ctx.Bitmap.GetCardinality()
	ctx.Scores = make(map[uint64]float64, cardinality)

	for tokenIdx := range field.Tokens {
		token := &field.Tokens[tokenIdx]

		pl, put := s.Storage.PostingLists[token.PostingListIndex].Bitmap()
		for plIt := pl.Iterator(); plIt.HasNext(); {
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
		put()
	}
}
