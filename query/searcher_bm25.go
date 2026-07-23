package query

import (
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
	ctx.Scoring.Reset(&ctx.Bitmap)

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

func CmpDocumentLengthEntryAndDocumentIndex(e storage.DocumentLengthEntry, t uint32) int {
	return int(e.Index) - int(t)
}

func CmpTokenFrequencyEntryAndDocumentIndex(e storage.TokenFrequencyEntry, t uint32) int {
	return int(e.DocumentIndex) - int(t)
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

		var guess int
		resolved := roaring.FastAnd(&ctx.Bitmap, &tokenPl).ToArray()
		switch {
		case freqDense && dlDense:
			var i int
			for ; i+4 < len(resolved); i += 4 {
				docIdx1 := resolved[i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]

				dl1 := float32(docLengths[docIdx1].Length) // Do inmediatly the index operation
				dl2 := float32(docLengths[docIdx2].Length) // Do inmediatly the index operation
				dl3 := float32(docLengths[docIdx3].Length) // Do inmediatly the index operation
				dl4 := float32(docLengths[docIdx4].Length) // Do inmediatly the index operation

				tf1 := float32(freqs[docIdx1].Frequency)
				tf2 := float32(freqs[docIdx2].Frequency)
				tf3 := float32(freqs[docIdx3].Frequency)
				tf4 := float32(freqs[docIdx4].Frequency)

				denominator1_1 := tf1 + satXOneMinuxLp
				denominator1_2 := tf2 + satXOneMinuxLp
				denominator1_3 := tf3 + satXOneMinuxLp
				denominator1_4 := tf4 + satXOneMinuxLp

				denominator2_1 := dl1 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_2 := dl2 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_3 := dl3 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_4 := dl4 * saturationXLengthPenaltyDivAvgDocLength

				tfnorm1 := tf1 / (denominator1_1 + denominator2_1)
				tfnorm2 := tf2 / (denominator1_2 + denominator2_2)
				tfnorm3 := tf3 / (denominator1_3 + denominator2_3)
				tfnorm4 := tf4 / (denominator1_4 + denominator2_4)

				score1 := idfBoost * tfnorm1
				score2 := idfBoost * tfnorm2
				score3 := idfBoost * tfnorm3
				score4 := idfBoost * tfnorm4

				ctx.Scoring.Add(guess, docIdx1, score1)
				ctx.Scoring.Add(guess, docIdx2, score2)
				ctx.Scoring.Add(guess, docIdx3, score3)
				guess = ctx.Scoring.Add(guess, docIdx4, score4)
			}

			for _, docIdx := range resolved[i:] {
				tf := float32(freqs[docIdx].Frequency)
				dl := float32(docLengths[docIdx].Length)

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)

				score := idfBoost * tfnorm

				guess = ctx.Scoring.Add(guess, docIdx, score)
			}
		case freqDense && !dlDense:
			var i int
			for ; i+4 < len(resolved); i += 4 {
				docIdx1 := resolved[i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]

				tf1 := float32(freqs[docIdx1].Frequency)
				tf2 := float32(freqs[docIdx2].Frequency)
				tf3 := float32(freqs[docIdx3].Frequency)
				tf4 := float32(freqs[docIdx4].Frequency)

				docLengthIdx4, _ := slices.BinarySearchFunc(docLengths, docIdx4, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx3, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx4], docIdx3, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx2, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx4], docIdx2, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx1, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx4], docIdx1, CmpDocumentLengthEntryAndDocumentIndex)

				dl1 := float32(docLengths[docLengthIdx1].Length)
				dl2 := float32(docLengths[docLengthIdx2].Length)
				dl3 := float32(docLengths[docLengthIdx3].Length)
				dl4 := float32(docLengths[docLengthIdx4].Length)
				docLengths = docLengths[1+docLengthIdx4:]

				denominator1_1 := tf1 + satXOneMinuxLp
				denominator1_2 := tf2 + satXOneMinuxLp
				denominator1_3 := tf3 + satXOneMinuxLp
				denominator1_4 := tf4 + satXOneMinuxLp

				denominator2_1 := dl1 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_2 := dl2 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_3 := dl3 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_4 := dl4 * saturationXLengthPenaltyDivAvgDocLength

				tfnorm1 := tf1 / (denominator1_1 + denominator2_1)
				tfnorm2 := tf2 / (denominator1_2 + denominator2_2)
				tfnorm3 := tf3 / (denominator1_3 + denominator2_3)
				tfnorm4 := tf4 / (denominator1_4 + denominator2_4)

				score1 := idfBoost * tfnorm1
				score2 := idfBoost * tfnorm2
				score3 := idfBoost * tfnorm3
				score4 := idfBoost * tfnorm4

				ctx.Scoring.Add(guess, docIdx1, score1)
				ctx.Scoring.Add(guess, docIdx2, score2)
				ctx.Scoring.Add(guess, docIdx3, score3)
				guess = ctx.Scoring.Add(guess, docIdx4, score4)
			}

			for _, docIdx := range resolved[i:] {
				tf := float32(freqs[docIdx].Frequency)
				docLengthIdx, _ := slices.BinarySearchFunc(docLengths, docIdx, CmpDocumentLengthEntryAndDocumentIndex)

				dl := float32(docLengths[docLengthIdx].Length)
				docLengths = docLengths[1+docLengthIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)

				score := idfBoost * tfnorm

				guess = ctx.Scoring.Add(guess, docIdx, score)
			}
		case !freqDense && dlDense:
			var i int
			for ; i+4 < len(resolved); i += 4 {
				docIdx1 := resolved[i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]

				dl1 := float32(docLengths[docIdx1].Length) // Do inmediatly the index operation
				dl2 := float32(docLengths[docIdx2].Length) // Do inmediatly the index operation
				dl3 := float32(docLengths[docIdx3].Length) // Do inmediatly the index operation
				dl4 := float32(docLengths[docIdx4].Length) // Do inmediatly the index operation

				freqIdx4, _ := slices.BinarySearchFunc(freqs, docIdx4, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx3, _ := slices.BinarySearchFunc(freqs[:freqIdx4], docIdx3, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx2, _ := slices.BinarySearchFunc(freqs[:freqIdx3], docIdx2, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx1, _ := slices.BinarySearchFunc(freqs[:freqIdx2], docIdx1, CmpTokenFrequencyEntryAndDocumentIndex)

				tf1 := float32(freqs[freqIdx1].Frequency)
				tf2 := float32(freqs[freqIdx2].Frequency)
				tf3 := float32(freqs[freqIdx3].Frequency)
				tf4 := float32(freqs[freqIdx4].Frequency)

				freqs = freqs[1+freqIdx4:]

				denominator1_1 := tf1 + satXOneMinuxLp
				denominator1_2 := tf2 + satXOneMinuxLp
				denominator1_3 := tf3 + satXOneMinuxLp
				denominator1_4 := tf4 + satXOneMinuxLp

				denominator2_1 := dl1 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_2 := dl2 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_3 := dl3 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_4 := dl4 * saturationXLengthPenaltyDivAvgDocLength

				tfnorm1 := tf1 / (denominator1_1 + denominator2_1)
				tfnorm2 := tf2 / (denominator1_2 + denominator2_2)
				tfnorm3 := tf3 / (denominator1_3 + denominator2_3)
				tfnorm4 := tf4 / (denominator1_4 + denominator2_4)

				score1 := idfBoost * tfnorm1
				score2 := idfBoost * tfnorm2
				score3 := idfBoost * tfnorm3
				score4 := idfBoost * tfnorm4

				ctx.Scoring.Add(guess, docIdx1, score1)
				ctx.Scoring.Add(guess, docIdx2, score2)
				ctx.Scoring.Add(guess, docIdx3, score3)
				guess = ctx.Scoring.Add(guess, docIdx4, score4)
			}

			for _, docIdx := range resolved[i:] {
				dl := float32(docLengths[docIdx].Length) // Do inmediatly the index operation
				freqIdx, _ := slices.BinarySearchFunc(freqs, docIdx, CmpTokenFrequencyEntryAndDocumentIndex)

				tf := float32(freqs[freqIdx].Frequency)
				freqs = freqs[1+freqIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)
				score := idfBoost * tfnorm

				guess = ctx.Scoring.Add(guess, docIdx, score)
			}
		default: // !freqDense && !dlDense
			var i int
			for ; i+4 < len(resolved); i += 4 {
				docIdx1 := resolved[i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]

				docLengthIdx4, _ := slices.BinarySearchFunc(docLengths, docIdx4, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx3, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx4], docIdx3, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx2, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx4], docIdx2, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx1, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx4], docIdx1, CmpDocumentLengthEntryAndDocumentIndex)

				dl1 := float32(docLengths[docLengthIdx1].Length)
				dl2 := float32(docLengths[docLengthIdx2].Length)
				dl3 := float32(docLengths[docLengthIdx3].Length)
				dl4 := float32(docLengths[docLengthIdx4].Length)
				docLengths = docLengths[1+docLengthIdx4:]

				freqIdx4, _ := slices.BinarySearchFunc(freqs, docIdx4, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx3, _ := slices.BinarySearchFunc(freqs[:freqIdx4], docIdx3, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx2, _ := slices.BinarySearchFunc(freqs[:freqIdx3], docIdx2, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx1, _ := slices.BinarySearchFunc(freqs[:freqIdx2], docIdx1, CmpTokenFrequencyEntryAndDocumentIndex)

				tf1 := float32(freqs[freqIdx1].Frequency)
				tf2 := float32(freqs[freqIdx2].Frequency)
				tf3 := float32(freqs[freqIdx3].Frequency)
				tf4 := float32(freqs[freqIdx4].Frequency)

				freqs = freqs[1+freqIdx4:]

				denominator1_1 := tf1 + satXOneMinuxLp
				denominator1_2 := tf2 + satXOneMinuxLp
				denominator1_3 := tf3 + satXOneMinuxLp
				denominator1_4 := tf4 + satXOneMinuxLp

				denominator2_1 := dl1 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_2 := dl2 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_3 := dl3 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_4 := dl4 * saturationXLengthPenaltyDivAvgDocLength

				tfnorm1 := tf1 / (denominator1_1 + denominator2_1)
				tfnorm2 := tf2 / (denominator1_2 + denominator2_2)
				tfnorm3 := tf3 / (denominator1_3 + denominator2_3)
				tfnorm4 := tf4 / (denominator1_4 + denominator2_4)

				score1 := idfBoost * tfnorm1
				score2 := idfBoost * tfnorm2
				score3 := idfBoost * tfnorm3
				score4 := idfBoost * tfnorm4

				ctx.Scoring.Add(guess, docIdx1, score1)
				ctx.Scoring.Add(guess, docIdx2, score2)
				ctx.Scoring.Add(guess, docIdx3, score3)
				guess = ctx.Scoring.Add(guess, docIdx4, score4)
			}

			for _, docIdx := range resolved[i:] {
				freqIdx, _ := slices.BinarySearchFunc(freqs, docIdx, CmpTokenFrequencyEntryAndDocumentIndex)
				docLengthIdx, _ := slices.BinarySearchFunc(docLengths, docIdx, CmpDocumentLengthEntryAndDocumentIndex)

				tf := float32(freqs[freqIdx].Frequency)
				freqs = freqs[1+freqIdx:]

				dl := float32(docLengths[docLengthIdx].Length)
				docLengths = docLengths[1+docLengthIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)

				score := idfBoost * tfnorm

				guess = ctx.Scoring.Add(guess, docIdx, score)
			}
		}
	}
}
