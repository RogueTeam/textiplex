package query

func (s *Searcher) FieldScore(ctx *QueryContext, fieldHash uint64) {
	field, found := s.Storage.Fields[fieldHash]
	if !found {
		return
	}

	cardinality := ctx.Bitmap.GetCardinality()
	ctx.Scores = make(map[uint64]float64, cardinality)

	it := field.Tokens.Iter()
	for valid := it.First(); valid; valid = it.Next() {
		token := it.Item()

		pl := s.Storage.PostingLists[token.PostingListIndex].Bitmap
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
	}
	it.Release()
}
