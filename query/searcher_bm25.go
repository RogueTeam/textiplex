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

	if q.Musts.Count() > 0 {
		s.Iter(&q.Musts, func(state *ClauseState) {
			s.accumulateBM25(ctx, state)
		})
	}
	if q.Shoulds.Count() > 0 {
		s.Iter(&q.Shoulds, func(state *ClauseState) {
			s.accumulateBM25(ctx, state)
		})
	}
}

const MinimumBM25Score = 0

func (s *Searcher) accumulateBM25(ctx *QueryContext, state *ClauseState) {
	var tokenPl roaring.Bitmap

	for _, token := range state.Tokens {
		docLengths := state.Field.DocumentLengths
		freqs := s.Storage.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

		avgDocLength := state.Field.AvgDocumentLength
		boost := state.Boost
		const satPlus1 = storage.DefaultSaturation + 1

		freqDense := len(freqs) == len(s.Storage.DocumentsIds)
		dlDense := len(docLengths) == len(s.Storage.DocumentsIds)

		// Hoist boost check: when boost==1 the multiply is a no-op, so we use a
		// dedicated idf-only multiplier and skip the extra float64 op in the hot path.
		var idfBoost float32
		if boost != 1 {
			idfBoost = token.Idf * boost * satPlus1
		} else {
			idfBoost = token.Idf * satPlus1
		}

		if idfBoost == 0 {
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
				tf := freqs[docIdx].Frequency
				dl := docLengths[docIdx].Length

				tfnorm := storage.NormalizedTF(tf, dl, avgDocLength)

				score := idfBoost * tfnorm
				if score > MinimumBM25Score {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		case freqDense && !dlDense:
			for _, docIdx := range resolved {
				tf := freqs[docIdx].Frequency
				docLengthIdx, _ := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })

				dl := docLengths[docLengthIdx].Length
				docLengths = docLengths[1+docLengthIdx:]

				tfnorm := storage.NormalizedTF(tf, dl, avgDocLength)

				score := idfBoost * tfnorm
				if score > MinimumBM25Score {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		case !freqDense && dlDense:
			for _, docIdx := range resolved {
				dl := docLengths[docIdx].Length // Do inmediatly the index operation
				freqIdx, _ := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })

				tf := freqs[freqIdx].Frequency
				freqs = freqs[1+freqIdx:]

				tfnorm := storage.NormalizedTF(tf, dl, avgDocLength)

				score := idfBoost * tfnorm
				if score > MinimumBM25Score {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		default: // !freqDense && !dlDense
			for _, docIdx := range resolved {
				freqIdx, _ := slices.BinarySearchFunc(freqs, docIdx, func(e storage.TokenFrequencyEntry, t uint32) int { return cmp.Compare(e.DocumentIndex, t) })
				docLengthIdx, _ := slices.BinarySearchFunc(docLengths, docIdx, func(e storage.DocumentLengthEntry, t uint32) int { return cmp.Compare(e.Index, t) })

				tf := freqs[freqIdx].Frequency
				freqs = freqs[1+freqIdx:]

				dl := docLengths[docLengthIdx].Length
				docLengths = docLengths[1+docLengthIdx:]

				tfnorm := storage.NormalizedTF(tf, dl, avgDocLength)

				score := idfBoost * tfnorm
				if score > MinimumBM25Score {
					ctx.Scores[docIdx] += idfBoost * tfnorm
				}
			}
		}
	}
}
