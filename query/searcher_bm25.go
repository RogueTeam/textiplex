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

	if q.Musts.Count() > 0 {
		s.Iter(&q.Musts, func(state *ClauseState) { s.accumulateBM25(ctx, state, saturation, lengthPenalty) })
	}
	if q.Shoulds.Count() > 0 {
		s.Iter(&q.Shoulds, func(state *ClauseState) { s.accumulateBM25(ctx, state, saturation, lengthPenalty) })
	}
}

func (s *Searcher) accumulateBM25(ctx *QueryContext, state *ClauseState, saturation, lengthPenalty float64) {
	if len(state.Tokens) == 0 {
		return
	}
	var tokenPl roaring.Bitmap
	for _, token := range state.Tokens {
		docLengths := state.Field.DocumentLengths
		freqs := s.Storage.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

		idf := InverseDocumentFrequency(uint64(len(state.Field.DocumentLengths)), token.FrequencyCount)
		avgDocLength := state.Field.AvgDocumentLength
		boost := state.Boost
		satPlus1 := saturation + 1
		oneMinusLP := 1 - lengthPenalty

		freqDense := len(freqs) == len(s.Storage.DocumentsIds)
		dlDense := len(docLengths) == len(s.Storage.DocumentsIds)

		// Hoist boost check: when boost==1 the multiply is a no-op, so we use a
		// dedicated idf-only multiplier and skip the extra float64 op in the hot path.
		var idfBoost float64
		if boost != 1 {
			idfBoost = idf * boost
		} else {
			idfBoost = idf
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
				tf := float64(freqs[docIdx].Frequency)
				dl := float64(docLengths[docIdx].Length)
				lengthRatio := dl / avgDocLength
				lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
				tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

				score := idfBoost * tfnorm
				if score > 0 {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		case freqDense && !dlDense:
			for _, docIdx := range resolved {
				tf := float64(freqs[docIdx].Frequency)

				docLengthIdx, found := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })
				if !found && docLengthIdx < len(docLengths) {
					docLengths = docLengths[docLengthIdx:]
					continue
				} else if !found {
					break
				}
				dl := float64(docLengths[docLengthIdx].Length)
				docLengths = docLengths[1+docLengthIdx:]

				lengthRatio := dl / avgDocLength
				lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
				tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

				score := idfBoost * tfnorm
				if score > 0 {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		case !freqDense && dlDense:
			for _, docIdx := range resolved {
				freqIdx, found := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })
				if !found && freqIdx < len(freqs) {
					freqs = freqs[freqIdx:]
					continue
				} else if !found {
					break
				}
				tf := float64(freqs[freqIdx].Frequency)
				freqs = freqs[1+freqIdx:]

				dl := float64(docLengths[docIdx].Length)
				lengthRatio := dl / avgDocLength
				lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
				tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

				score := idfBoost * tfnorm
				if score > 0 {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		default: // !freqDense && !dlDense
			for _, docIdx := range resolved {
				freqIdx, found := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })
				if !found && freqIdx < len(freqs) {
					freqs = freqs[freqIdx:]
					continue
				} else if !found {
					break
				}
				tf := float64(freqs[freqIdx].Frequency)
				freqs = freqs[1+freqIdx:]

				docLengthIdx, found := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })
				if !found && docLengthIdx < len(docLengths) {
					docLengths = docLengths[docLengthIdx:]
					continue
				} else if !found {
					break
				}
				dl := float64(docLengths[docLengthIdx].Length)
				docLengths = docLengths[1+docLengthIdx:]

				lengthRatio := dl / avgDocLength
				lengthNorm := oneMinusLP + lengthPenalty*lengthRatio
				tfnorm := (tf * satPlus1) / (tf + saturation*lengthNorm)

				score := idfBoost * tfnorm
				if score > 0 {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		}
	}
}
