package query

import (
	"simd/archsimd"
	"slices"

	"github.com/RoaringBitmap/roaring"
)

func (s *Searcher) AVX2AccumulateBM25(ctx *QueryContext, state *ClauseState, saturation, lengthPenalty float32) {
	var tokenPl roaring.Bitmap

	docLengths := state.Field.DocumentLengths
	avgDocLength := state.Field.AvgDocumentLength
	boost := state.Boost
	satPlus1 := saturation + 1
	oneMinusLP := 1 - lengthPenalty

	satXOneMinuxLp := saturation * oneMinusLP
	satXoneMinuxLpVec := [8]float32{
		satXOneMinuxLp,
		satXOneMinuxLp,
		satXOneMinuxLp,
		satXOneMinuxLp,
		satXOneMinuxLp,
		satXOneMinuxLp,
		satXOneMinuxLp,
		satXOneMinuxLp,
	}
	satXoneMinuxLps := archsimd.LoadFloat32x8Array(&satXoneMinuxLpVec)

	saturationXLengthPenaltyDivAvgDocLength := saturation * (lengthPenalty / avgDocLength)
	saturationXLengthPenaltyDivAvgDocLengthVec := [8]float32{
		saturationXLengthPenaltyDivAvgDocLength,
		saturationXLengthPenaltyDivAvgDocLength,
		saturationXLengthPenaltyDivAvgDocLength,
		saturationXLengthPenaltyDivAvgDocLength,
		saturationXLengthPenaltyDivAvgDocLength,
		saturationXLengthPenaltyDivAvgDocLength,
		saturationXLengthPenaltyDivAvgDocLength,
		saturationXLengthPenaltyDivAvgDocLength,
	}
	saturationXLengthPenaltyDivAvgDocLengths := archsimd.LoadFloat32x8Array(&saturationXLengthPenaltyDivAvgDocLengthVec)

	dlDense := len(docLengths) == len(s.Storage.DocumentsIds)

	var dlsVec, tfsVec, scoresOut [8]float32

	for _, token := range state.Tokens {
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

		idfBoostVec := [8]float32{idfBoost, idfBoost, idfBoost, idfBoost, idfBoost, idfBoost, idfBoost, idfBoost}
		idfBoosts := archsimd.LoadFloat32x8Array(&idfBoostVec)

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

				dlsVec[0] = float32(docLengths[docIdx1].Length) // Do inmediatly the index operation
				dlsVec[1] = float32(docLengths[docIdx2].Length) // Do inmediatly the index operation
				dlsVec[2] = float32(docLengths[docIdx3].Length) // Do inmediatly the index operation
				dlsVec[3] = float32(docLengths[docIdx4].Length) // Do inmediatly the index operation
				dlsVec[4] = float32(docLengths[docIdx5].Length) // Do inmediatly the index operation
				dlsVec[5] = float32(docLengths[docIdx6].Length) // Do inmediatly the index operation
				dlsVec[6] = float32(docLengths[docIdx7].Length) // Do inmediatly the index operation
				dlsVec[7] = float32(docLengths[docIdx8].Length) // Do inmediatly the index operation

				tfsVec[0] = float32(freqs[docIdx1].Frequency)
				tfsVec[1] = float32(freqs[docIdx2].Frequency)
				tfsVec[2] = float32(freqs[docIdx3].Frequency)
				tfsVec[3] = float32(freqs[docIdx4].Frequency)
				tfsVec[4] = float32(freqs[docIdx5].Frequency)
				tfsVec[5] = float32(freqs[docIdx6].Frequency)
				tfsVec[6] = float32(freqs[docIdx7].Frequency)
				tfsVec[7] = float32(freqs[docIdx8].Frequency)

				dls := archsimd.LoadFloat32x8Array(&dlsVec)
				tfs := archsimd.LoadFloat32x8Array(&tfsVec)

				denominator1 := tfs.Add(satXoneMinuxLps)

				denominator := dls.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, denominator1)
				reciprocal := denominator.Reciprocal()

				tfNorms := tfs.Mul(reciprocal)

				scores := idfBoosts.Mul(tfNorms)

				scores.StoreArray(&scoresOut)

				guess = ctx.Scoring.Add(guess, docIdx1, scoresOut[0])
				guess = ctx.Scoring.Add(guess, docIdx2, scoresOut[1])
				guess = ctx.Scoring.Add(guess, docIdx3, scoresOut[2])
				guess = ctx.Scoring.Add(guess, docIdx4, scoresOut[3])
				guess = ctx.Scoring.Add(guess, docIdx5, scoresOut[4])
				guess = ctx.Scoring.Add(guess, docIdx6, scoresOut[5])
				guess = ctx.Scoring.Add(guess, docIdx7, scoresOut[6])
				guess = ctx.Scoring.Add(guess, docIdx8, scoresOut[7])
			}

			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

				tf := float32(freqs[docIdx].Frequency)
				dl := float32(docLengths[docIdx].Length)

				denominator1 := tf + satXOneMinuxLp
				denominator2 := dl * saturationXLengthPenaltyDivAvgDocLength
				tfnorm := tf / (denominator1 + denominator2)

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

				tfsVec[0] = float32(freqs[docIdx1].Frequency)
				tfsVec[1] = float32(freqs[docIdx2].Frequency)
				tfsVec[2] = float32(freqs[docIdx3].Frequency)
				tfsVec[3] = float32(freqs[docIdx4].Frequency)
				tfsVec[4] = float32(freqs[docIdx5].Frequency)
				tfsVec[5] = float32(freqs[docIdx6].Frequency)
				tfsVec[6] = float32(freqs[docIdx7].Frequency)
				tfsVec[7] = float32(freqs[docIdx8].Frequency)

				docLengthIdx8, _ := slices.BinarySearchFunc(docLengths, docIdx8, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx7, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx8], docIdx7, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx6, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx7], docIdx6, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx5, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx6], docIdx5, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx4, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx5], docIdx4, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx3, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx4], docIdx3, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx2, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx3], docIdx2, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx1, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx2], docIdx1, CmpDocumentLengthEntryAndDocumentIndex)

				dlsVec[0] = float32(docLengths[docLengthIdx1].Length)
				dlsVec[1] = float32(docLengths[docLengthIdx2].Length)
				dlsVec[2] = float32(docLengths[docLengthIdx3].Length)
				dlsVec[3] = float32(docLengths[docLengthIdx4].Length)
				dlsVec[4] = float32(docLengths[docLengthIdx5].Length)
				dlsVec[5] = float32(docLengths[docLengthIdx6].Length)
				dlsVec[6] = float32(docLengths[docLengthIdx7].Length)
				dlsVec[7] = float32(docLengths[docLengthIdx8].Length)
				docLengths = docLengths[1+docLengthIdx8:]

				dls := archsimd.LoadFloat32x8Array(&dlsVec)
				tfs := archsimd.LoadFloat32x8Array(&tfsVec)

				denominator1 := tfs.Add(satXoneMinuxLps)

				denominator := dls.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, denominator1)
				reciprocal := denominator.Reciprocal()

				tfNorms := tfs.Mul(reciprocal)

				scores := idfBoosts.Mul(tfNorms)

				scores.StoreArray(&scoresOut)

				guess = ctx.Scoring.Add(guess, docIdx1, scoresOut[0])
				guess = ctx.Scoring.Add(guess, docIdx2, scoresOut[1])
				guess = ctx.Scoring.Add(guess, docIdx3, scoresOut[2])
				guess = ctx.Scoring.Add(guess, docIdx4, scoresOut[3])
				guess = ctx.Scoring.Add(guess, docIdx5, scoresOut[4])
				guess = ctx.Scoring.Add(guess, docIdx6, scoresOut[5])
				guess = ctx.Scoring.Add(guess, docIdx7, scoresOut[6])
				guess = ctx.Scoring.Add(guess, docIdx8, scoresOut[7])
			}

			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

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
			for i := 0; i < n8; i += UnrollingFactor {
				docIdx1 := resolved[0+i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]
				docIdx5 := resolved[4+i]
				docIdx6 := resolved[5+i]
				docIdx7 := resolved[6+i]
				docIdx8 := resolved[7+i]

				dlsVec[0] = float32(docLengths[docIdx1].Length) // Do inmediatly the index operation
				dlsVec[1] = float32(docLengths[docIdx2].Length) // Do inmediatly the index operation
				dlsVec[2] = float32(docLengths[docIdx3].Length) // Do inmediatly the index operation
				dlsVec[3] = float32(docLengths[docIdx4].Length) // Do inmediatly the index operation
				dlsVec[4] = float32(docLengths[docIdx5].Length) // Do inmediatly the index operation
				dlsVec[5] = float32(docLengths[docIdx6].Length) // Do inmediatly the index operation
				dlsVec[6] = float32(docLengths[docIdx7].Length) // Do inmediatly the index operation
				dlsVec[7] = float32(docLengths[docIdx8].Length) // Do inmediatly the index operation

				freqIdx8, _ := slices.BinarySearchFunc(freqs, docIdx8, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx7, _ := slices.BinarySearchFunc(freqs[:freqIdx8], docIdx7, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx6, _ := slices.BinarySearchFunc(freqs[:freqIdx7], docIdx6, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx5, _ := slices.BinarySearchFunc(freqs[:freqIdx6], docIdx5, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx4, _ := slices.BinarySearchFunc(freqs[:freqIdx5], docIdx4, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx3, _ := slices.BinarySearchFunc(freqs[:freqIdx4], docIdx3, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx2, _ := slices.BinarySearchFunc(freqs[:freqIdx3], docIdx2, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx1, _ := slices.BinarySearchFunc(freqs[:freqIdx2], docIdx1, CmpTokenFrequencyEntryAndDocumentIndex)

				tfsVec[0] = float32(freqs[freqIdx1].Frequency)
				tfsVec[1] = float32(freqs[freqIdx2].Frequency)
				tfsVec[2] = float32(freqs[freqIdx3].Frequency)
				tfsVec[3] = float32(freqs[freqIdx4].Frequency)
				tfsVec[4] = float32(freqs[freqIdx5].Frequency)
				tfsVec[5] = float32(freqs[freqIdx6].Frequency)
				tfsVec[6] = float32(freqs[freqIdx7].Frequency)
				tfsVec[7] = float32(freqs[freqIdx8].Frequency)
				freqs = freqs[1+freqIdx8:]

				dls := archsimd.LoadFloat32x8Array(&dlsVec)
				tfs := archsimd.LoadFloat32x8Array(&tfsVec)

				denominator1 := tfs.Add(satXoneMinuxLps)

				denominator := dls.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, denominator1)
				reciprocal := denominator.Reciprocal()

				tfNorms := tfs.Mul(reciprocal)

				scores := idfBoosts.Mul(tfNorms)

				scores.StoreArray(&scoresOut)

				guess = ctx.Scoring.Add(guess, docIdx1, scoresOut[0])
				guess = ctx.Scoring.Add(guess, docIdx2, scoresOut[1])
				guess = ctx.Scoring.Add(guess, docIdx3, scoresOut[2])
				guess = ctx.Scoring.Add(guess, docIdx4, scoresOut[3])
				guess = ctx.Scoring.Add(guess, docIdx5, scoresOut[4])
				guess = ctx.Scoring.Add(guess, docIdx6, scoresOut[5])
				guess = ctx.Scoring.Add(guess, docIdx7, scoresOut[6])
				guess = ctx.Scoring.Add(guess, docIdx8, scoresOut[7])
			}

			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

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

			for i := 0; i < n8; i += UnrollingFactor {
				docIdx1 := resolved[0+i]
				docIdx2 := resolved[1+i]
				docIdx3 := resolved[2+i]
				docIdx4 := resolved[3+i]
				docIdx5 := resolved[4+i]
				docIdx6 := resolved[5+i]
				docIdx7 := resolved[6+i]
				docIdx8 := resolved[7+i]

				docLengthIdx8, _ := slices.BinarySearchFunc(docLengths, docIdx8, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx7, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx8], docIdx7, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx6, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx7], docIdx6, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx5, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx6], docIdx5, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx4, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx5], docIdx4, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx3, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx4], docIdx3, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx2, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx3], docIdx2, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx1, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx2], docIdx1, CmpDocumentLengthEntryAndDocumentIndex)

				dlsVec[0] = float32(docLengths[docLengthIdx1].Length)
				dlsVec[1] = float32(docLengths[docLengthIdx2].Length)
				dlsVec[2] = float32(docLengths[docLengthIdx3].Length)
				dlsVec[3] = float32(docLengths[docLengthIdx4].Length)
				dlsVec[4] = float32(docLengths[docLengthIdx5].Length)
				dlsVec[5] = float32(docLengths[docLengthIdx6].Length)
				dlsVec[6] = float32(docLengths[docLengthIdx7].Length)
				dlsVec[7] = float32(docLengths[docLengthIdx8].Length)
				docLengths = docLengths[1+docLengthIdx8:]

				freqIdx8, _ := slices.BinarySearchFunc(freqs, docIdx8, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx7, _ := slices.BinarySearchFunc(freqs[:freqIdx8], docIdx7, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx6, _ := slices.BinarySearchFunc(freqs[:freqIdx7], docIdx6, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx5, _ := slices.BinarySearchFunc(freqs[:freqIdx6], docIdx5, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx4, _ := slices.BinarySearchFunc(freqs[:freqIdx5], docIdx4, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx3, _ := slices.BinarySearchFunc(freqs[:freqIdx4], docIdx3, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx2, _ := slices.BinarySearchFunc(freqs[:freqIdx3], docIdx2, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx1, _ := slices.BinarySearchFunc(freqs[:freqIdx2], docIdx1, CmpTokenFrequencyEntryAndDocumentIndex)

				tfsVec[0] = float32(freqs[freqIdx1].Frequency)
				tfsVec[1] = float32(freqs[freqIdx2].Frequency)
				tfsVec[2] = float32(freqs[freqIdx3].Frequency)
				tfsVec[3] = float32(freqs[freqIdx4].Frequency)
				tfsVec[4] = float32(freqs[freqIdx5].Frequency)
				tfsVec[5] = float32(freqs[freqIdx6].Frequency)
				tfsVec[6] = float32(freqs[freqIdx7].Frequency)
				tfsVec[7] = float32(freqs[freqIdx8].Frequency)
				freqs = freqs[1+freqIdx8:]

				dls := archsimd.LoadFloat32x8Array(&dlsVec)
				tfs := archsimd.LoadFloat32x8Array(&tfsVec)

				denominator1 := tfs.Add(satXoneMinuxLps)

				denominator := dls.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, denominator1)
				reciprocal := denominator.Reciprocal()

				tfNorms := tfs.Mul(reciprocal)

				scores := idfBoosts.Mul(tfNorms)

				scores.StoreArray(&scoresOut)

				guess = ctx.Scoring.Add(guess, docIdx1, scoresOut[0])
				guess = ctx.Scoring.Add(guess, docIdx2, scoresOut[1])
				guess = ctx.Scoring.Add(guess, docIdx3, scoresOut[2])
				guess = ctx.Scoring.Add(guess, docIdx4, scoresOut[3])
				guess = ctx.Scoring.Add(guess, docIdx5, scoresOut[4])
				guess = ctx.Scoring.Add(guess, docIdx6, scoresOut[5])
				guess = ctx.Scoring.Add(guess, docIdx7, scoresOut[6])
				guess = ctx.Scoring.Add(guess, docIdx8, scoresOut[7])
			}

			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

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
