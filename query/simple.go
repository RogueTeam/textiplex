package query

import (
	"cmp"
	"slices"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/zeebo/xxh3"
)

type SimpleQuery struct {
	Shoulds Clause
	Musts   Clause
	// Must not will not make use of boost
	MustNots Clause
}

func NewSimpleQuery() *SimpleQuery {
	return &SimpleQuery{}
}

// Query context intended to be cached and reused by caller on each search
type QueryContext struct {
	Bitmap roaring64.Bitmap
	Scores map[uint64]float64
}

func (ctx *QueryContext) UpdateScores(s *storage.Storage, state *ClauseState) {
	token := state.Token
	field := state.Field

	tokenHash := xxh3.Hash(token.Value)

	fieldsTokenDocsKey := storage.Tuple3[uint64]{A: state.FieldHash, B: tokenHash}
	fieldDocsKey := storage.Tuple2[uint64]{A: state.FieldHash}

	for it := ctx.Bitmap.Iterator(); it.HasNext(); {
		docIdx := it.Next()

		fieldsTokenDocsKey.C = docIdx
		freq, found := s.FieldTokenDocFrequencies[fieldsTokenDocsKey.Hash()]
		if !found {
			continue
		}

		fieldDocsKey.B = docIdx
		docLength, found := s.FieldDocLengths[fieldDocsKey.Hash()]
		if !found {
			continue
		}

		scoreDelta := ScoreTermBM25(
			/* docCoun */ uint64(len(field.DocumentLengths)),
			/* tokenDocFreq */ token.FrequencyCount,
			/* tokenFreq */ freq,
			/* documentLength */ docLength,
			/* avgDocLength */ field.AvgDocumentLength,
			/* saturation */ DefaultSaturation,
			/* lengthPenalty */ DefaultLengthPenalty,
		)

		if state.Keyword != nil {
			ctx.Scores[docIdx] += state.Keyword.Boost * scoreDelta
		} else if state.Range != nil {
			ctx.Scores[docIdx] += state.Range.Boost * scoreDelta
		} else {
			// Should never match but is good guard to unknown cases
			ctx.Scores[docIdx] += scoreDelta
		}
	}
}

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (q *SimpleQuery) FilterDocuments(ctx *QueryContext, s *storage.Storage) {
	mustsCount := q.Musts.Count()
	shouldsCount := q.Shoulds.Count()
	mustNotsCount := q.MustNots.Count()

	if mustsCount > 0 {
		// Musts define the candidate set: intersection of all Must posting lists.
		var firstMust bool
		q.Musts.Iter(ctx, s, func(state *ClauseState) {
			pl := &s.PostingLists[state.Token.PostingListIndex].Bitmap
			if !firstMust {
				ctx.Bitmap.Or(pl)
				firstMust = true
			} else {
				ctx.Bitmap.And(pl)
			}
		})
	} else if shouldsCount > 0 {
		// No Musts: Shoulds define the set (union of Should posting lists).
		q.Shoulds.Iter(ctx, s, func(state *ClauseState) {
			ctx.Bitmap.Or(&s.PostingLists[state.Token.PostingListIndex].Bitmap)
		})
	}

	if mustNotsCount > 0 {
		// MustNots subtract from whatever the set is.
		q.MustNots.Iter(ctx, s, func(state *ClauseState) {
			ctx.Bitmap.AndNot(&s.PostingLists[state.Token.PostingListIndex].Bitmap)
		})
	}

	ctx.Scores = make(map[uint64]float64, ctx.Bitmap.GetCardinality())

	if mustsCount > 0 {
		q.Musts.Iter(ctx, s, func(state *ClauseState) { ctx.UpdateScores(s, state) })
	}
	if shouldsCount > 0 {
		q.Shoulds.Iter(ctx, s, func(state *ClauseState) { ctx.UpdateScores(s, state) })
	}
}

// Once a filtering is done scoring is the next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (q *SimpleQuery) ResolveBM25(ctx *QueryContext) (idxs []uint64) {
	type bm25 struct {
		docIdx uint64
		score  float64
	}

	scores := make([]bm25, 0, ctx.Bitmap.GetCardinality())

	it := ctx.Bitmap.Iterator()
	for it.HasNext() {
		doxIdx := it.Next()

		score := ctx.Scores[doxIdx]
		if score == 0 {
			continue
		}

		scores = append(scores, bm25{
			score:  score,
			docIdx: doxIdx,
		})
	}

	slices.SortFunc(
		scores,
		func(a, b bm25) int {
			scoreCmp := cmp.Compare(b.score, a.score)
			if scoreCmp == 0 {
				return cmp.Compare(b.docIdx, a.docIdx)
			}
			return scoreCmp
		},
	)

	idxs = make([]uint64, len(scores))
	for index := range scores {
		idxs[index] = scores[index].docIdx
	}
	return idxs
}
