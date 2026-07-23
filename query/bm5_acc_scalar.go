package query

import (
	"github.com/RoaringBitmap/roaring"
)

const (
	UnrollingFactor = 8 // AVX 256-bit (32 bytes) = 8 x sizeof(float32)
	UnrollMask      = UnrollingFactor - 1
)

func (s *Searcher) ScalarAccumulateBM25(ctx *QueryContext, state *ClauseState, saturation, lengthPenalty float32) {
	var tokenPl roaring.Bitmap

	avgDocLength := state.Field.AvgDocumentLength
	boost := state.Boost
	satPlus1 := saturation + 1
	oneMinusLP := 1 - lengthPenalty
	satXOneMinuxLp := saturation * oneMinusLP
	saturationXLengthPenaltyDivAvgDocLength := saturation * (lengthPenalty / avgDocLength)
	dlDense := len(state.Field.DocumentLengths) == len(s.Storage.DocumentsIds)

	for _, token := range state.Tokens {
		docLengths := state.Field.DocumentLengths

		idf := InverseDocumentFrequency(uint64(len(state.Field.DocumentLengths)), token.FrequencyCount)
		freqs := s.Storage.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]
		freqDense := len(freqs) == len(s.Storage.DocumentsIds)

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
		n8 := len(resolved) &^ UnrollMask
		switch {
		case freqDense && dlDense:
			for i := 0; i < n8; i += UnrollingFactor {
				docIdx1 := resolved[0+i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]
				docIdx5 := resolved[4+i]
				docIdx6 := resolved[5+i]
				docIdx7 := resolved[6+i]
				docIdx8 := resolved[7+i]

				dl1 := float32(docLengths[docIdx1].Length) // Do inmediatly the index operation
				dl2 := float32(docLengths[docIdx2].Length) // Do inmediatly the index operation
				dl3 := float32(docLengths[docIdx3].Length) // Do inmediatly the index operation
				dl4 := float32(docLengths[docIdx4].Length) // Do inmediatly the index operation
				dl5 := float32(docLengths[docIdx5].Length) // Do inmediatly the index operation
				dl6 := float32(docLengths[docIdx6].Length) // Do inmediatly the index operation
				dl7 := float32(docLengths[docIdx7].Length) // Do inmediatly the index operation
				dl8 := float32(docLengths[docIdx8].Length) // Do inmediatly the index operation

				tf1 := float32(freqs[docIdx1].Frequency)
				tf2 := float32(freqs[docIdx2].Frequency)
				tf3 := float32(freqs[docIdx3].Frequency)
				tf4 := float32(freqs[docIdx4].Frequency)
				tf5 := float32(freqs[docIdx5].Frequency)
				tf6 := float32(freqs[docIdx6].Frequency)
				tf7 := float32(freqs[docIdx7].Frequency)
				tf8 := float32(freqs[docIdx8].Frequency)

				denominator1_1 := tf1 + satXOneMinuxLp
				denominator1_2 := tf2 + satXOneMinuxLp
				denominator1_3 := tf3 + satXOneMinuxLp
				denominator1_4 := tf4 + satXOneMinuxLp
				denominator1_5 := tf5 + satXOneMinuxLp
				denominator1_6 := tf6 + satXOneMinuxLp
				denominator1_7 := tf7 + satXOneMinuxLp
				denominator1_8 := tf8 + satXOneMinuxLp

				denominator2_1 := dl1 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_2 := dl2 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_3 := dl3 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_4 := dl4 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_5 := dl5 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_6 := dl6 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_7 := dl7 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_8 := dl8 * saturationXLengthPenaltyDivAvgDocLength

				denominator1 := denominator1_1 + denominator2_1
				denominator2 := denominator1_2 + denominator2_2
				denominator3 := denominator1_3 + denominator2_3
				denominator4 := denominator1_4 + denominator2_4
				denominator5 := denominator1_5 + denominator2_5
				denominator6 := denominator1_6 + denominator2_6
				denominator7 := denominator1_7 + denominator2_7
				denominator8 := denominator1_8 + denominator2_8

				reciprocal1 := 1 / denominator1
				reciprocal2 := 1 / denominator2
				reciprocal3 := 1 / denominator3
				reciprocal4 := 1 / denominator4
				reciprocal5 := 1 / denominator5
				reciprocal6 := 1 / denominator6
				reciprocal7 := 1 / denominator7
				reciprocal8 := 1 / denominator8

				tfnorm1 := tf1 * reciprocal1
				tfnorm2 := tf2 * reciprocal2
				tfnorm3 := tf3 * reciprocal3
				tfnorm4 := tf4 * reciprocal4
				tfnorm5 := tf5 * reciprocal5
				tfnorm6 := tf6 * reciprocal6
				tfnorm7 := tf7 * reciprocal7
				tfnorm8 := tf8 * reciprocal8

				score1 := idfBoost * tfnorm1
				score2 := idfBoost * tfnorm2
				score3 := idfBoost * tfnorm3
				score4 := idfBoost * tfnorm4
				score5 := idfBoost * tfnorm5
				score6 := idfBoost * tfnorm6
				score7 := idfBoost * tfnorm7
				score8 := idfBoost * tfnorm8

				guess = ctx.Scoring.Add(guess, docIdx1, score1)
				guess = ctx.Scoring.Add(guess, docIdx2, score2)
				guess = ctx.Scoring.Add(guess, docIdx3, score3)
				guess = ctx.Scoring.Add(guess, docIdx4, score4)
				guess = ctx.Scoring.Add(guess, docIdx5, score5)
				guess = ctx.Scoring.Add(guess, docIdx6, score6)
				guess = ctx.Scoring.Add(guess, docIdx7, score7)
				guess = ctx.Scoring.Add(guess, docIdx8, score8)
			}

			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

				tf := float32(freqs[docIdx].Frequency)
				dl := float32(docLengths[docIdx].Length)

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				denominator := denominator1 + denominator2
				reciprocal := 1 / denominator
				tfnorm := tf * reciprocal

				score := idfBoost * tfnorm

				guess = ctx.Scoring.Add(guess, docIdx, score)
			}
		case freqDense && !dlDense:
			for i := 0; i < n8; i += UnrollingFactor {
				docIdx1 := resolved[0+i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]
				docIdx5 := resolved[4+i]
				docIdx6 := resolved[5+i]
				docIdx7 := resolved[6+i]
				docIdx8 := resolved[7+i]

				tf1 := float32(freqs[docIdx1].Frequency)
				tf2 := float32(freqs[docIdx2].Frequency)
				tf3 := float32(freqs[docIdx3].Frequency)
				tf4 := float32(freqs[docIdx4].Frequency)
				tf5 := float32(freqs[docIdx5].Frequency)
				tf6 := float32(freqs[docIdx6].Frequency)
				tf7 := float32(freqs[docIdx7].Frequency)
				tf8 := float32(freqs[docIdx8].Frequency)

				docLengthIdx8, _ := docLengths.BinarySearch(docIdx8)
				docLengthIdx7, _ := docLengths[:docLengthIdx8].BinarySearch(docIdx7)
				docLengthIdx6, _ := docLengths[:docLengthIdx7].BinarySearch(docIdx6)
				docLengthIdx5, _ := docLengths[:docLengthIdx6].BinarySearch(docIdx5)
				docLengthIdx4, _ := docLengths[:docLengthIdx5].BinarySearch(docIdx4)
				docLengthIdx3, _ := docLengths[:docLengthIdx4].BinarySearch(docIdx3)
				docLengthIdx2, _ := docLengths[:docLengthIdx3].BinarySearch(docIdx2)
				docLengthIdx1, _ := docLengths[:docLengthIdx2].BinarySearch(docIdx1)

				dl1 := float32(docLengths[docLengthIdx1].Length)
				dl2 := float32(docLengths[docLengthIdx2].Length)
				dl3 := float32(docLengths[docLengthIdx3].Length)
				dl4 := float32(docLengths[docLengthIdx4].Length)
				dl5 := float32(docLengths[docLengthIdx5].Length)
				dl6 := float32(docLengths[docLengthIdx6].Length)
				dl7 := float32(docLengths[docLengthIdx7].Length)
				dl8 := float32(docLengths[docLengthIdx8].Length)
				docLengths = docLengths[1+docLengthIdx8:]

				denominator1_1 := tf1 + satXOneMinuxLp
				denominator1_2 := tf2 + satXOneMinuxLp
				denominator1_3 := tf3 + satXOneMinuxLp
				denominator1_4 := tf4 + satXOneMinuxLp
				denominator1_5 := tf5 + satXOneMinuxLp
				denominator1_6 := tf6 + satXOneMinuxLp
				denominator1_7 := tf7 + satXOneMinuxLp
				denominator1_8 := tf8 + satXOneMinuxLp

				denominator2_1 := dl1 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_2 := dl2 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_3 := dl3 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_4 := dl4 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_5 := dl5 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_6 := dl6 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_7 := dl7 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_8 := dl8 * saturationXLengthPenaltyDivAvgDocLength

				denominator1 := denominator1_1 + denominator2_1
				denominator2 := denominator1_2 + denominator2_2
				denominator3 := denominator1_3 + denominator2_3
				denominator4 := denominator1_4 + denominator2_4
				denominator5 := denominator1_5 + denominator2_5
				denominator6 := denominator1_6 + denominator2_6
				denominator7 := denominator1_7 + denominator2_7
				denominator8 := denominator1_8 + denominator2_8

				reciprocal1 := 1 / denominator1
				reciprocal2 := 1 / denominator2
				reciprocal3 := 1 / denominator3
				reciprocal4 := 1 / denominator4
				reciprocal5 := 1 / denominator5
				reciprocal6 := 1 / denominator6
				reciprocal7 := 1 / denominator7
				reciprocal8 := 1 / denominator8

				tfnorm1 := tf1 * reciprocal1
				tfnorm2 := tf2 * reciprocal2
				tfnorm3 := tf3 * reciprocal3
				tfnorm4 := tf4 * reciprocal4
				tfnorm5 := tf5 * reciprocal5
				tfnorm6 := tf6 * reciprocal6
				tfnorm7 := tf7 * reciprocal7
				tfnorm8 := tf8 * reciprocal8

				score1 := idfBoost * tfnorm1
				score2 := idfBoost * tfnorm2
				score3 := idfBoost * tfnorm3
				score4 := idfBoost * tfnorm4
				score5 := idfBoost * tfnorm5
				score6 := idfBoost * tfnorm6
				score7 := idfBoost * tfnorm7
				score8 := idfBoost * tfnorm8

				guess = ctx.Scoring.Add(guess, docIdx1, score1)
				guess = ctx.Scoring.Add(guess, docIdx2, score2)
				guess = ctx.Scoring.Add(guess, docIdx3, score3)
				guess = ctx.Scoring.Add(guess, docIdx4, score4)
				guess = ctx.Scoring.Add(guess, docIdx5, score5)
				guess = ctx.Scoring.Add(guess, docIdx6, score6)
				guess = ctx.Scoring.Add(guess, docIdx7, score7)
				guess = ctx.Scoring.Add(guess, docIdx8, score8)
			}

			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

				tf := float32(freqs[docIdx].Frequency)
				docLengthIdx, _ := docLengths.BinarySearch(docIdx)

				dl := float32(docLengths[docLengthIdx].Length)
				docLengths = docLengths[1+docLengthIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				denominator := denominator1 + denominator2
				reciprocal := 1 / denominator
				tfnorm := tf * reciprocal

				score := idfBoost * tfnorm

				guess = ctx.Scoring.Add(guess, docIdx, score)
			}
		case !freqDense && dlDense:
			for i := 0; i < n8; i += UnrollingFactor {
				docIdx1 := resolved[0+i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]
				docIdx5 := resolved[4+i]
				docIdx6 := resolved[5+i]
				docIdx7 := resolved[6+i]
				docIdx8 := resolved[7+i]

				dl1 := float32(docLengths[docIdx1].Length) // Do inmediatly the index operation
				dl2 := float32(docLengths[docIdx2].Length) // Do inmediatly the index operation
				dl3 := float32(docLengths[docIdx3].Length) // Do inmediatly the index operation
				dl4 := float32(docLengths[docIdx4].Length) // Do inmediatly the index operation
				dl5 := float32(docLengths[docIdx5].Length) // Do inmediatly the index operation
				dl6 := float32(docLengths[docIdx6].Length) // Do inmediatly the index operation
				dl7 := float32(docLengths[docIdx7].Length) // Do inmediatly the index operation
				dl8 := float32(docLengths[docIdx8].Length) // Do inmediatly the index operation

				freqIdx8, _ := freqs.BinarySearch(docIdx8)
				freqIdx7, _ := freqs[:freqIdx8].BinarySearch(docIdx7)
				freqIdx6, _ := freqs[:freqIdx7].BinarySearch(docIdx6)
				freqIdx5, _ := freqs[:freqIdx6].BinarySearch(docIdx5)
				freqIdx4, _ := freqs[:freqIdx5].BinarySearch(docIdx4)
				freqIdx3, _ := freqs[:freqIdx4].BinarySearch(docIdx3)
				freqIdx2, _ := freqs[:freqIdx3].BinarySearch(docIdx2)
				freqIdx1, _ := freqs[:freqIdx2].BinarySearch(docIdx1)

				tf1 := float32(freqs[freqIdx1].Frequency)
				tf2 := float32(freqs[freqIdx2].Frequency)
				tf3 := float32(freqs[freqIdx3].Frequency)
				tf4 := float32(freqs[freqIdx4].Frequency)
				tf5 := float32(freqs[freqIdx5].Frequency)
				tf6 := float32(freqs[freqIdx6].Frequency)
				tf7 := float32(freqs[freqIdx7].Frequency)
				tf8 := float32(freqs[freqIdx8].Frequency)

				freqs = freqs[1+freqIdx8:]

				denominator1_1 := tf1 + satXOneMinuxLp
				denominator1_2 := tf2 + satXOneMinuxLp
				denominator1_3 := tf3 + satXOneMinuxLp
				denominator1_4 := tf4 + satXOneMinuxLp
				denominator1_5 := tf5 + satXOneMinuxLp
				denominator1_6 := tf6 + satXOneMinuxLp
				denominator1_7 := tf7 + satXOneMinuxLp
				denominator1_8 := tf8 + satXOneMinuxLp

				denominator2_1 := dl1 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_2 := dl2 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_3 := dl3 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_4 := dl4 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_5 := dl5 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_6 := dl6 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_7 := dl7 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_8 := dl8 * saturationXLengthPenaltyDivAvgDocLength

				denominator1 := denominator1_1 + denominator2_1
				denominator2 := denominator1_2 + denominator2_2
				denominator3 := denominator1_3 + denominator2_3
				denominator4 := denominator1_4 + denominator2_4
				denominator5 := denominator1_5 + denominator2_5
				denominator6 := denominator1_6 + denominator2_6
				denominator7 := denominator1_7 + denominator2_7
				denominator8 := denominator1_8 + denominator2_8

				reciprocal1 := 1 / denominator1
				reciprocal2 := 1 / denominator2
				reciprocal3 := 1 / denominator3
				reciprocal4 := 1 / denominator4
				reciprocal5 := 1 / denominator5
				reciprocal6 := 1 / denominator6
				reciprocal7 := 1 / denominator7
				reciprocal8 := 1 / denominator8

				tfnorm1 := tf1 * reciprocal1
				tfnorm2 := tf2 * reciprocal2
				tfnorm3 := tf3 * reciprocal3
				tfnorm4 := tf4 * reciprocal4
				tfnorm5 := tf5 * reciprocal5
				tfnorm6 := tf6 * reciprocal6
				tfnorm7 := tf7 * reciprocal7
				tfnorm8 := tf8 * reciprocal8

				score1 := idfBoost * tfnorm1
				score2 := idfBoost * tfnorm2
				score3 := idfBoost * tfnorm3
				score4 := idfBoost * tfnorm4
				score5 := idfBoost * tfnorm5
				score6 := idfBoost * tfnorm6
				score7 := idfBoost * tfnorm7
				score8 := idfBoost * tfnorm8

				guess = ctx.Scoring.Add(guess, docIdx1, score1)
				guess = ctx.Scoring.Add(guess, docIdx2, score2)
				guess = ctx.Scoring.Add(guess, docIdx3, score3)
				guess = ctx.Scoring.Add(guess, docIdx4, score4)
				guess = ctx.Scoring.Add(guess, docIdx5, score5)
				guess = ctx.Scoring.Add(guess, docIdx6, score6)
				guess = ctx.Scoring.Add(guess, docIdx7, score7)
				guess = ctx.Scoring.Add(guess, docIdx8, score8)
			}

			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

				dl := float32(docLengths[docIdx].Length) // Do inmediatly the index operation
				freqIdx, _ := freqs.BinarySearch(docIdx)

				tf := float32(freqs[freqIdx].Frequency)
				freqs = freqs[1+freqIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				denominator := denominator1 + denominator2
				reciprocal := 1 / denominator
				tfnorm := tf * reciprocal

				score := idfBoost * tfnorm

				guess = ctx.Scoring.Add(guess, docIdx, score)
			}
		default: // !freqDense && !dlDense
			for i := 0; i < n8; i += UnrollingFactor {
				docIdx1 := resolved[0+i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]
				docIdx5 := resolved[4+i]
				docIdx6 := resolved[5+i]
				docIdx7 := resolved[6+i]
				docIdx8 := resolved[7+i]

				docLengthIdx8, _ := docLengths.BinarySearch(docIdx8)
				docLengthIdx7, _ := docLengths[:docLengthIdx8].BinarySearch(docIdx7)
				docLengthIdx6, _ := docLengths[:docLengthIdx7].BinarySearch(docIdx6)
				docLengthIdx5, _ := docLengths[:docLengthIdx6].BinarySearch(docIdx5)
				docLengthIdx4, _ := docLengths[:docLengthIdx5].BinarySearch(docIdx4)
				docLengthIdx3, _ := docLengths[:docLengthIdx4].BinarySearch(docIdx3)
				docLengthIdx2, _ := docLengths[:docLengthIdx3].BinarySearch(docIdx2)
				docLengthIdx1, _ := docLengths[:docLengthIdx2].BinarySearch(docIdx1)

				dl1 := float32(docLengths[docLengthIdx1].Length)
				dl2 := float32(docLengths[docLengthIdx2].Length)
				dl3 := float32(docLengths[docLengthIdx3].Length)
				dl4 := float32(docLengths[docLengthIdx4].Length)
				dl5 := float32(docLengths[docLengthIdx5].Length)
				dl6 := float32(docLengths[docLengthIdx6].Length)
				dl7 := float32(docLengths[docLengthIdx7].Length)
				dl8 := float32(docLengths[docLengthIdx8].Length)
				docLengths = docLengths[1+docLengthIdx8:]

				freqIdx8, _ := freqs.BinarySearch(docIdx8)
				freqIdx7, _ := freqs[:freqIdx8].BinarySearch(docIdx7)
				freqIdx6, _ := freqs[:freqIdx7].BinarySearch(docIdx6)
				freqIdx5, _ := freqs[:freqIdx6].BinarySearch(docIdx5)
				freqIdx4, _ := freqs[:freqIdx5].BinarySearch(docIdx4)
				freqIdx3, _ := freqs[:freqIdx4].BinarySearch(docIdx3)
				freqIdx2, _ := freqs[:freqIdx3].BinarySearch(docIdx2)
				freqIdx1, _ := freqs[:freqIdx2].BinarySearch(docIdx1)

				tf1 := float32(freqs[freqIdx1].Frequency)
				tf2 := float32(freqs[freqIdx2].Frequency)
				tf3 := float32(freqs[freqIdx3].Frequency)
				tf4 := float32(freqs[freqIdx4].Frequency)
				tf5 := float32(freqs[freqIdx5].Frequency)
				tf6 := float32(freqs[freqIdx6].Frequency)
				tf7 := float32(freqs[freqIdx7].Frequency)
				tf8 := float32(freqs[freqIdx8].Frequency)

				freqs = freqs[1+freqIdx8:]

				denominator1_1 := tf1 + satXOneMinuxLp
				denominator1_2 := tf2 + satXOneMinuxLp
				denominator1_3 := tf3 + satXOneMinuxLp
				denominator1_4 := tf4 + satXOneMinuxLp
				denominator1_5 := tf5 + satXOneMinuxLp
				denominator1_6 := tf6 + satXOneMinuxLp
				denominator1_7 := tf7 + satXOneMinuxLp
				denominator1_8 := tf8 + satXOneMinuxLp

				denominator2_1 := dl1 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_2 := dl2 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_3 := dl3 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_4 := dl4 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_5 := dl5 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_6 := dl6 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_7 := dl7 * saturationXLengthPenaltyDivAvgDocLength
				denominator2_8 := dl8 * saturationXLengthPenaltyDivAvgDocLength

				denominator1 := denominator1_1 + denominator2_1
				denominator2 := denominator1_2 + denominator2_2
				denominator3 := denominator1_3 + denominator2_3
				denominator4 := denominator1_4 + denominator2_4
				denominator5 := denominator1_5 + denominator2_5
				denominator6 := denominator1_6 + denominator2_6
				denominator7 := denominator1_7 + denominator2_7
				denominator8 := denominator1_8 + denominator2_8

				reciprocal1 := 1 / denominator1
				reciprocal2 := 1 / denominator2
				reciprocal3 := 1 / denominator3
				reciprocal4 := 1 / denominator4
				reciprocal5 := 1 / denominator5
				reciprocal6 := 1 / denominator6
				reciprocal7 := 1 / denominator7
				reciprocal8 := 1 / denominator8

				tfnorm1 := tf1 * reciprocal1
				tfnorm2 := tf2 * reciprocal2
				tfnorm3 := tf3 * reciprocal3
				tfnorm4 := tf4 * reciprocal4
				tfnorm5 := tf5 * reciprocal5
				tfnorm6 := tf6 * reciprocal6
				tfnorm7 := tf7 * reciprocal7
				tfnorm8 := tf8 * reciprocal8

				score1 := idfBoost * tfnorm1
				score2 := idfBoost * tfnorm2
				score3 := idfBoost * tfnorm3
				score4 := idfBoost * tfnorm4
				score5 := idfBoost * tfnorm5
				score6 := idfBoost * tfnorm6
				score7 := idfBoost * tfnorm7
				score8 := idfBoost * tfnorm8

				guess = ctx.Scoring.Add(guess, docIdx1, score1)
				guess = ctx.Scoring.Add(guess, docIdx2, score2)
				guess = ctx.Scoring.Add(guess, docIdx3, score3)
				guess = ctx.Scoring.Add(guess, docIdx4, score4)
				guess = ctx.Scoring.Add(guess, docIdx5, score5)
				guess = ctx.Scoring.Add(guess, docIdx6, score6)
				guess = ctx.Scoring.Add(guess, docIdx7, score7)
				guess = ctx.Scoring.Add(guess, docIdx8, score8)
			}

			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

				freqIdx, _ := freqs.BinarySearch(docIdx)
				docLengthIdx, _ := docLengths.BinarySearch(docIdx)

				tf := float32(freqs[freqIdx].Frequency)
				freqs = freqs[1+freqIdx:]

				dl := float32(docLengths[docLengthIdx].Length)
				docLengths = docLengths[1+docLengthIdx:]

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				denominator := denominator1 + denominator2
				reciprocal := 1 / denominator
				tfnorm := tf * reciprocal

				score := idfBoost * tfnorm

				guess = ctx.Scoring.Add(guess, docIdx, score)
			}
		}
	}
}
