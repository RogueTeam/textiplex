package query

import (
	"cmp"
	"slices"

	"github.com/RogueTeam/textiplex/storage"
)

func (s *Searcher) BM25Score(ctx *QueryContext, q *SimpleQuery) {
	ctx.Scores = make(map[uint32]float64, ctx.Bitmap.GetCardinality())

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

	var saturation, lengthPenalty float64
	if s.BM25Saturation != 0 {
		saturation = s.BM25Saturation
	} else {
		saturation = DefaultSaturation
	}

	if s.BM25LengthPenalty != 0 {
		lengthPenalty = s.BM25LengthPenalty
	} else {
		lengthPenalty = DefaultLengthPenalty
	}

	var docIdxs [ManyIteratorBatchSize]uint32
	it := ctx.Bitmap.ManyIterator()
loop:
	for {
		n := it.NextMany(docIdxs[:])

		for _, docIdx := range docIdxs[:n] {
			var freq uint64
			if len(freqs) == len(s.Storage.DocumentsIds) {
				freq = freqs[docIdx].Frequency
			} else {
				freqIdx, found := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })
				if !found && freqIdx < len(freqs) {
					freqs = freqs[freqIdx:]
					continue
				} else if !found {
					break loop
				}
				freq = freqs[freqIdx].Frequency
				freqs = freqs[1+freqIdx:]
			}

			var docLength uint64
			if len(docLengths) == len(s.Storage.DocumentsIds) {
				docLength = docLengths[docIdx].Length
			} else {
				docLengthIdx, found := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })
				if !found && docLengthIdx < len(docLengths) {
					docLengths = docLengths[docLengthIdx:]
					continue
				} else if !found {
					break loop
				}
				docLength = docLengths[docLengthIdx].Length
				docLengths = docLengths[1+docLengthIdx:]
			}

			ctx.Scores[docIdx] += state.Boost * ScoreTermBM25(
				/* docCoun */ uint64(len(field.DocumentLengths)),
				/* tokenDocFreq */ token.FrequencyCount,
				/* tokenFreq */ freq,
				/* documentLength */ docLength,
				/* avgDocLength */ field.AvgDocumentLength,
				/* saturation */ saturation,
				/* lengthPenalty */ lengthPenalty,
			)
		}

		if n < len(docIdxs) {
			break
		}
	}
}
