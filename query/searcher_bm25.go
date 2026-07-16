package query

import (
	"cmp"
	"slices"

	"github.com/RoaringBitmap/roaring"
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
	ctx.Scores = make(map[uint32]float32, cardinality)
	if cardinality == 0 {
		return
	}

	var saturation, lengthPenalty float32
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

	if q.Musts.Count() > 0 {
		s.Iter(&q.Musts, func(state *ClauseState) {
			s.accumulateBM25(ctx, state, saturation, lengthPenalty)
		})
	}
	if q.Shoulds.Count() > 0 {
		s.Iter(&q.Shoulds, func(state *ClauseState) {
			s.accumulateBM25(ctx, state, saturation, lengthPenalty)
		})
	}
}

const MinimumBM25Score = 0

func (s *Searcher) accumulateBM25(ctx *QueryContext, state *ClauseState, saturation, lengthPenalty float32) {
	var tokenPl roaring.Bitmap

	for _, token := range state.Tokens {
		docLengths := state.Field.DocumentLengths
		freqs := s.Storage.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

		idf := InverseDocumentFrequency(uint64(len(state.Field.DocumentLengths)), token.FrequencyCount)
		avgDocLength := state.Field.AvgDocumentLength
		boost := state.Boost
		satPlus1 := saturation + 1
		oneMinusLP := 1 - lengthPenalty
		satXOneMinuxLp := saturation * oneMinusLP

		saturationXLengthPenaltyDivAvgDocLength := saturation * (lengthPenalty / avgDocLength)

		freqDense := len(freqs) == len(s.Storage.DocumentsIds)
		dlDense := len(docLengths) == len(s.Storage.DocumentsIds)

		// Hoist boost check: when boost==1 the multiply is a no-op, so we use a
		// dedicated idf-only multiplier and skip the extra float64 op in the hot path.
		var idfBoost float32
		if boost != 1 {
			idfBoost = idf * boost * satPlus1
		} else {
			idfBoost = idf * satPlus1
		}

		if idfBoost == 0 || satPlus1 == 0 {
			continue
		}

		s.Storage.PostingLists[token.PostingListIndex].UnsafeBitmap(&tokenPl)
		if tokenPl.IsEmpty() {
			continue
		}

		resolved := roaring.FastAnd(&ctx.Bitmap, &tokenPl).ToArray()
		switch {
		case freqDense && dlDense:
			for _, docIdx := range resolved {
				tf := float32(freqs[docIdx].Frequency)
				dl := float32(docLengths[docIdx].Length)

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)

				score := idfBoost * tfnorm
				if score > MinimumBM25Score {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		case freqDense && !dlDense:
			for _, docIdx := range resolved {
				tf := float32(freqs[docIdx].Frequency)
				docLengthIdx, _ := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })

				dl := float32(docLengths[docLengthIdx].Length)
				docLengths = docLengths[1+docLengthIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)

				score := idfBoost * tfnorm
				if score > MinimumBM25Score {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		case !freqDense && dlDense:
			for _, docIdx := range resolved {
				dl := float32(docLengths[docIdx].Length) // Do inmediatly the index operation
				freqIdx, _ := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })

				tf := float32(freqs[freqIdx].Frequency)
				freqs = freqs[1+freqIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)

				score := idfBoost * tfnorm
				if score > MinimumBM25Score {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		default: // !freqDense && !dlDense
			for _, docIdx := range resolved {
				freqIdx, _ := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })
				docLengthIdx, _ := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })

				tf := float32(freqs[freqIdx].Frequency)
				freqs = freqs[1+freqIdx:]

				dl := float32(docLengths[docLengthIdx].Length)
				docLengths = docLengths[1+docLengthIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)

				score := idfBoost * tfnorm
				if score > MinimumBM25Score {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		}
	}
}
