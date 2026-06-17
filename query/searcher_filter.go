package query

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (s *Searcher) FilterDocuments(ctx *QueryContext, q *SimpleQuery) {
	mustsCount := q.Musts.Count()
	shouldsCount := q.Shoulds.Count()
	mustNotsCount := q.MustNots.Count()

	if mustsCount > 0 {
		// Musts define the candidate set: intersection of all Must posting lists.
		var firstMust bool
		s.Iter(&q.Musts, func(state *ClauseState) {
			if !state.Found {
				ctx.Bitmap.Clear()
				return
			}

			pl, put := s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap()
			put()
			if !firstMust {
				ctx.Bitmap.Or(pl)
				firstMust = true
			} else {
				ctx.Bitmap.And(pl)
			}
		})
	} else if shouldsCount > 0 {
		// No Musts: Shoulds define the set (union of Should posting lists).
		s.Iter(&q.Shoulds, func(state *ClauseState) {
			if !state.Found {
				return
			}

			pl, put := s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap()
			defer put()
			ctx.Bitmap.Or(pl)
		})
	}

	if mustNotsCount > 0 {
		// MustNots subtract from whatever the set is.
		s.Iter(&q.MustNots, func(state *ClauseState) {
			if !state.Found {
				return
			}

			pl, put := s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap()
			defer put()
			ctx.Bitmap.AndNot(pl)
		})
	}
}
