package query

import (
	"cmp"
	"slices"

	"github.com/RogueTeam/textiplex/storage"
)

func (s *Searcher) BM25Score(ctx *QueryContext, q *SimpleQuery) {
	ctx.Scores = make(map[uint64]float64, ctx.Bitmap.GetCardinality())

	if q.Musts.Count() > 0 {
		s.Iter(&q.Musts, func(state *ClauseState) { s.UpdateScoresWithBM25(ctx, state) })
	}
	if q.Shoulds.Count() > 0 {
		s.Iter(&q.Shoulds, func(state *ClauseState) { s.UpdateScoresWithBM25(ctx, state) })
	}
}

func (s *Searcher) UpdateScoresWithBM25(ctx *QueryContext, state *ClauseState) {
	if !state.Found {
		return
	}
	token := state.Token
	field := state.Field

	docLengths := field.DocumentLengths
	freqs := s.Storage.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

	for it := ctx.Bitmap.Iterator(); it.HasNext(); {
		docIdx := it.Next()

		docLengthIdx, found := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint64) int { return cmp.Compare(e.Index, t) })
		if !found {
			continue
		}
		docLength := docLengths[docLengthIdx].Length
		docLengths = docLengths[1+docLengthIdx:]

		freqIdx, found := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint64) int { return cmp.Compare(e.DocumentIndex, t) })
		if !found {
			continue
		}
		freq := freqs[freqIdx].Frequency
		freqs = freqs[1+freqIdx:]

		ctx.Scores[docIdx] += state.Boost * ScoreTermBM25(
			/* docCoun */ uint64(len(field.DocumentLengths)),
			/* tokenDocFreq */ token.FrequencyCount,
			/* tokenFreq */ freq,
			/* documentLength */ docLength,
			/* avgDocLength */ field.AvgDocumentLength,
			/* saturation */ DefaultSaturation,
			/* lengthPenalty */ DefaultLengthPenalty,
		)
	}
}

// Once a filtering and scoring are done, next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (s *Searcher) ResolveBM25(ctx *QueryContext) (idxs []uint64) {
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
