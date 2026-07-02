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
// The hot path materializes the bitmap once into an ascending candidates
// slice and accumulates into a dense []float64 aligned to it, rather than
// writing the score map on every term-document pair. The bitmap is immutable
// during scoring, so position i in candidates is the same document across
// every term walk. That single ToArray call replaces one iterator allocation
// and one heap batch buffer per term walk, turns every walk into a plain
// slice range, lets every per-term write land on a slice instead of a hashed
// map, and collapses the map writes into one final pass.
func (s *Searcher) BM25Score(ctx *QueryContext, q *SimpleQuery) {
	cardinality := ctx.Bitmap.GetCardinality()
	ctx.Scores = make(map[uint32]float64, cardinality)
	if cardinality == 0 {
		return
	}

	mustsCount := q.Musts.Count()
	shouldsCount := q.Shoulds.Count()
	if mustsCount == 0 && shouldsCount == 0 {
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

	candidates := ctx.Bitmap.ToArray()
	acc := make([]float64, len(candidates))

	if mustsCount > 0 {
		s.Iter(&q.Musts, func(state *ClauseState) { s.accumulateBM25(candidates, acc, state, saturation, lengthPenalty) })
	}
	if shouldsCount > 0 {
		s.Iter(&q.Shoulds, func(state *ClauseState) { s.accumulateBM25(candidates, acc, state, saturation, lengthPenalty) })
	}

	// Materialize once. A position stays zero only when no term contributed or
	// every contribution was scaled by a zero boost; both read back as the map's
	// 0.0 default, and ResolveScores already discards score==0, so skipping the
	// write is observationally identical to writing a 0.0 entry.
	for i, docIdx := range candidates {
		if acc[i] != 0 {
			ctx.Scores[docIdx] = acc[i]
		}
	}
}

func (s *Searcher) accumulateBM25(candidates []uint32, acc []float64, state *ClauseState, saturation, lengthPenalty float64) {
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

	switch {
	case freqDense && dlDense:
		for i, docIdx := range candidates {
			var freq = freqs[docIdx].Frequency
			var docLength = docLengths[docIdx].Length

			// Inlined NormalizedTF: identical operations, identical grouping.
			tf := float64(freq)
			dl := float64(docLength)
			lengthRatio := dl / avgDocLength
			lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
			tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

			acc[i] += boost * (idf * tfnorm)
		}
	case freqDense && !dlDense:
		for i, docIdx := range candidates {
			var freq = freqs[docIdx].Frequency

			docLengthIdx, found := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })
			if !found && docLengthIdx < len(docLengths) {
				docLengths = docLengths[docLengthIdx:]
				continue
			} else if !found {
				break
			}
			docLength := docLengths[docLengthIdx].Length
			docLengths = docLengths[1+docLengthIdx:]

			// Inlined NormalizedTF: identical operations, identical grouping.
			tf := float64(freq)
			dl := float64(docLength)
			lengthRatio := dl / avgDocLength
			lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
			tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

			acc[i] += boost * (idf * tfnorm)
		}
	case !freqDense && dlDense:
		for i, docIdx := range candidates {
			freqIdx, found := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })
			if !found && freqIdx < len(freqs) {
				freqs = freqs[freqIdx:]
				continue
			} else if !found {
				break
			}
			freq := freqs[freqIdx].Frequency
			freqs = freqs[1+freqIdx:]

			var docLength = docLengths[docIdx].Length

			// Inlined NormalizedTF: identical operations, identical grouping.
			tf := float64(freq)
			dl := float64(docLength)
			lengthRatio := dl / avgDocLength
			lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
			tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

			acc[i] += boost * (idf * tfnorm)
		}
	default: // !freqDense && !dlDense:
		for i, docIdx := range candidates {
			freqIdx, found := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })
			if !found && freqIdx < len(freqs) {
				freqs = freqs[freqIdx:]
				continue
			} else if !found {
				break
			}
			freq := freqs[freqIdx].Frequency
			freqs = freqs[1+freqIdx:]

			docLengthIdx, found := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })
			if !found && docLengthIdx < len(docLengths) {
				docLengths = docLengths[docLengthIdx:]
				continue
			} else if !found {
				break
			}
			docLength := docLengths[docLengthIdx].Length
			docLengths = docLengths[1+docLengthIdx:]

			// Inlined NormalizedTF: identical operations, identical grouping.
			tf := float64(freq)
			dl := float64(docLength)
			lengthRatio := dl / avgDocLength
			lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
			tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

			acc[i] += boost * (idf * tfnorm)
		}
	}

}
