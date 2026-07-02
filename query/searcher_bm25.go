package query

import (
	"cmp"
	"slices"

	"github.com/RogueTeam/textiplex/storage"
)

// BM25Score fills ctx.Scores with the BM25 score of every document in
// ctx.Bitmap. Musts are scored first, then Shoulds, matching the original
// summation order so the floating point result is bit-for-bit identical.
//
// The hot path accumulates into a dense []float64 keyed by the bitmap's
// ascending iteration position rather than writing the score map on every
// term-document pair. roaring's ManyIterator yields document indexes in
// ascending order and the bitmap is immutable during scoring, so the i-th
// document visited is the same document across every term walk. That makes the
// compact index a stable alias for the document index, lets every per-term
// write land on a slice instead of a hashed map, and collapses the map writes
// into one final pass.
func (s *Searcher) BM25Score(ctx *QueryContext, q *SimpleQuery) {
	cardinality := ctx.Bitmap.GetCardinality()
	ctx.Scores = make(map[uint32]float64, cardinality)
	if cardinality == 0 {
		return
	}

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

	acc := make([]float64, cardinality)

	if q.Musts.Count() > 0 {
		s.Iter(&q.Musts, func(state *ClauseState) { s.accumulateBM25(ctx, acc, state, saturation, lengthPenalty) })
	}
	if q.Shoulds.Count() > 0 {
		s.Iter(&q.Shoulds, func(state *ClauseState) { s.accumulateBM25(ctx, acc, state, saturation, lengthPenalty) })
	}

	// Materialize once. A position stays zero only when no term contributed or
	// every contribution was scaled by a zero boost; both read back as the map's
	// 0.0 default, and ResolveScores already discards score==0, so skipping the
	// write is observationally identical to writing a 0.0 entry.
	var docIdxs [ManyIteratorBatchSize]uint32
	it := ctx.Bitmap.ManyIterator()
	i := 0
	for {
		n := it.NextMany(docIdxs[:])
		for _, docIdx := range docIdxs[:n] {
			if acc[i] != 0 {
				ctx.Scores[docIdx] = acc[i]
			}
			i++
		}
		if n < len(docIdxs) {
			break
		}
	}
}

func (s *Searcher) accumulateBM25(ctx *QueryContext, acc []float64, state *ClauseState, saturation, lengthPenalty float64) {
	if !state.Found {
		return
	}
	token := state.Token
	field := state.Field

	docLengths := field.DocumentLengths
	freqs := s.Storage.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

	idf := InverseDocumentFrequency(uint64(len(field.DocumentLengths)), token.FrequencyCount)
	avgDocLength := field.AvgDocumentLength
	boost := state.Boost
	satPlus1 := saturation + 1
	oneMinusLP := 1 - lengthPenalty

	freqDense := len(freqs) == len(s.Storage.DocumentsIds)
	dlDense := len(docLengths) == len(s.Storage.DocumentsIds)

	var docIdxs [ManyIteratorBatchSize]uint32
	it := ctx.Bitmap.ManyIterator()
	i := 0
loop:
	for {
		n := it.NextMany(docIdxs[:])

		for _, docIdx := range docIdxs[:n] {
			var freq uint64
			if freqDense {
				freq = freqs[docIdx].Frequency
			} else {
				freqIdx, found := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })
				if !found && freqIdx < len(freqs) {
					freqs = freqs[freqIdx:]
					i++
					continue
				} else if !found {
					break loop
				}
				freq = freqs[freqIdx].Frequency
				freqs = freqs[1+freqIdx:]
			}

			var docLength uint64
			if dlDense {
				docLength = docLengths[docIdx].Length
			} else {
				docLengthIdx, found := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })
				if !found && docLengthIdx < len(docLengths) {
					docLengths = docLengths[docLengthIdx:]
					i++
					continue
				} else if !found {
					break loop
				}
				docLength = docLengths[docLengthIdx].Length
				docLengths = docLengths[1+docLengthIdx:]
			}

			// Inlined NormalizedTF: identical operations, identical grouping.
			tf := float64(freq)
			dl := float64(docLength)
			lengthRatio := dl / avgDocLength
			lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
			tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

			acc[i] += boost * (idf * tfnorm)
			i++
		}

		if n < len(docIdxs) {
			break
		}
	}
}
