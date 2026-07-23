package query

import (
	"simd/archsimd"
	"slices"

	"github.com/RoaringBitmap/roaring"
)

const (
	// LaneCountAvx2 is the width of one AVX2 vector: 256-bit (32 bytes) = 8 x sizeof(float32).
	LaneCountAvx2 = 8
	LaneMaskAvx2  = LaneCountAvx2 - 1

	// UnrollingFactorAvx2 is how many documents one iteration of the hot loop
	// consumes: 8 independent Float32x8 vectors chained back to back.
	UnrollingFactorAvx2 = 64
	UnrollMaskAvx2      = UnrollingFactorAvx2 - 1

	VectorsPerUnrollAvx2 = UnrollingFactorAvx2 / LaneCountAvx2
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

	// One staging array per in-flight vector. These live on the stack, so the
	// 64-wide gather phase costs no vector registers: it only issues the 64
	// independent scalar loads that the SIMD phase then consumes 8 at a time.
	var (
		dlsVec1, dlsVec2, dlsVec3, dlsVec4 [8]float32
		dlsVec5, dlsVec6, dlsVec7, dlsVec8 [8]float32

		tfsVec1, tfsVec2, tfsVec3, tfsVec4 [8]float32
		tfsVec5, tfsVec6, tfsVec7, tfsVec8 [8]float32

		scoresOut1, scoresOut2, scoresOut3, scoresOut4 [8]float32
		scoresOut5, scoresOut6, scoresOut7, scoresOut8 [8]float32
	)

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

		// n64 bounds the 64-wide unrolled loop, n8 bounds the single-vector
		// remainder loop, and whatever is left over is finished scalar.
		n64 := len(resolved) &^ UnrollMaskAvx2
		n8 := len(resolved) &^ LaneMaskAvx2

		switch {
		case freqDense && dlDense:
			// 64 documents per iteration: 8 x Float32x8.
			for i := 0; i < n64; i += UnrollingFactorAvx2 {
				docIdx01, docIdx02, docIdx03, docIdx04 := resolved[0+i], resolved[1+i], resolved[2+i], resolved[3+i]
				docIdx05, docIdx06, docIdx07, docIdx08 := resolved[4+i], resolved[5+i], resolved[6+i], resolved[7+i]
				docIdx09, docIdx10, docIdx11, docIdx12 := resolved[8+i], resolved[9+i], resolved[10+i], resolved[11+i]
				docIdx13, docIdx14, docIdx15, docIdx16 := resolved[12+i], resolved[13+i], resolved[14+i], resolved[15+i]
				docIdx17, docIdx18, docIdx19, docIdx20 := resolved[16+i], resolved[17+i], resolved[18+i], resolved[19+i]
				docIdx21, docIdx22, docIdx23, docIdx24 := resolved[20+i], resolved[21+i], resolved[22+i], resolved[23+i]
				docIdx25, docIdx26, docIdx27, docIdx28 := resolved[24+i], resolved[25+i], resolved[26+i], resolved[27+i]
				docIdx29, docIdx30, docIdx31, docIdx32 := resolved[28+i], resolved[29+i], resolved[30+i], resolved[31+i]
				docIdx33, docIdx34, docIdx35, docIdx36 := resolved[32+i], resolved[33+i], resolved[34+i], resolved[35+i]
				docIdx37, docIdx38, docIdx39, docIdx40 := resolved[36+i], resolved[37+i], resolved[38+i], resolved[39+i]
				docIdx41, docIdx42, docIdx43, docIdx44 := resolved[40+i], resolved[41+i], resolved[42+i], resolved[43+i]
				docIdx45, docIdx46, docIdx47, docIdx48 := resolved[44+i], resolved[45+i], resolved[46+i], resolved[47+i]
				docIdx49, docIdx50, docIdx51, docIdx52 := resolved[48+i], resolved[49+i], resolved[50+i], resolved[51+i]
				docIdx53, docIdx54, docIdx55, docIdx56 := resolved[52+i], resolved[53+i], resolved[54+i], resolved[55+i]
				docIdx57, docIdx58, docIdx59, docIdx60 := resolved[56+i], resolved[57+i], resolved[58+i], resolved[59+i]
				docIdx61, docIdx62, docIdx63, docIdx64 := resolved[60+i], resolved[61+i], resolved[62+i], resolved[63+i]

				// Dense document lengths: index directly, no probe needed.
				dlsVec1[0], dlsVec1[1] = float32(docLengths[docIdx01].Length), float32(docLengths[docIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docIdx03].Length), float32(docLengths[docIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docIdx05].Length), float32(docLengths[docIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docIdx07].Length), float32(docLengths[docIdx08].Length)
				dlsVec2[0], dlsVec2[1] = float32(docLengths[docIdx09].Length), float32(docLengths[docIdx10].Length)
				dlsVec2[2], dlsVec2[3] = float32(docLengths[docIdx11].Length), float32(docLengths[docIdx12].Length)
				dlsVec2[4], dlsVec2[5] = float32(docLengths[docIdx13].Length), float32(docLengths[docIdx14].Length)
				dlsVec2[6], dlsVec2[7] = float32(docLengths[docIdx15].Length), float32(docLengths[docIdx16].Length)
				dlsVec3[0], dlsVec3[1] = float32(docLengths[docIdx17].Length), float32(docLengths[docIdx18].Length)
				dlsVec3[2], dlsVec3[3] = float32(docLengths[docIdx19].Length), float32(docLengths[docIdx20].Length)
				dlsVec3[4], dlsVec3[5] = float32(docLengths[docIdx21].Length), float32(docLengths[docIdx22].Length)
				dlsVec3[6], dlsVec3[7] = float32(docLengths[docIdx23].Length), float32(docLengths[docIdx24].Length)
				dlsVec4[0], dlsVec4[1] = float32(docLengths[docIdx25].Length), float32(docLengths[docIdx26].Length)
				dlsVec4[2], dlsVec4[3] = float32(docLengths[docIdx27].Length), float32(docLengths[docIdx28].Length)
				dlsVec4[4], dlsVec4[5] = float32(docLengths[docIdx29].Length), float32(docLengths[docIdx30].Length)
				dlsVec4[6], dlsVec4[7] = float32(docLengths[docIdx31].Length), float32(docLengths[docIdx32].Length)
				dlsVec5[0], dlsVec5[1] = float32(docLengths[docIdx33].Length), float32(docLengths[docIdx34].Length)
				dlsVec5[2], dlsVec5[3] = float32(docLengths[docIdx35].Length), float32(docLengths[docIdx36].Length)
				dlsVec5[4], dlsVec5[5] = float32(docLengths[docIdx37].Length), float32(docLengths[docIdx38].Length)
				dlsVec5[6], dlsVec5[7] = float32(docLengths[docIdx39].Length), float32(docLengths[docIdx40].Length)
				dlsVec6[0], dlsVec6[1] = float32(docLengths[docIdx41].Length), float32(docLengths[docIdx42].Length)
				dlsVec6[2], dlsVec6[3] = float32(docLengths[docIdx43].Length), float32(docLengths[docIdx44].Length)
				dlsVec6[4], dlsVec6[5] = float32(docLengths[docIdx45].Length), float32(docLengths[docIdx46].Length)
				dlsVec6[6], dlsVec6[7] = float32(docLengths[docIdx47].Length), float32(docLengths[docIdx48].Length)
				dlsVec7[0], dlsVec7[1] = float32(docLengths[docIdx49].Length), float32(docLengths[docIdx50].Length)
				dlsVec7[2], dlsVec7[3] = float32(docLengths[docIdx51].Length), float32(docLengths[docIdx52].Length)
				dlsVec7[4], dlsVec7[5] = float32(docLengths[docIdx53].Length), float32(docLengths[docIdx54].Length)
				dlsVec7[6], dlsVec7[7] = float32(docLengths[docIdx55].Length), float32(docLengths[docIdx56].Length)
				dlsVec8[0], dlsVec8[1] = float32(docLengths[docIdx57].Length), float32(docLengths[docIdx58].Length)
				dlsVec8[2], dlsVec8[3] = float32(docLengths[docIdx59].Length), float32(docLengths[docIdx60].Length)
				dlsVec8[4], dlsVec8[5] = float32(docLengths[docIdx61].Length), float32(docLengths[docIdx62].Length)
				dlsVec8[6], dlsVec8[7] = float32(docLengths[docIdx63].Length), float32(docLengths[docIdx64].Length)

				// Dense token frequencies: index directly, no probe needed.
				tfsVec1[0], tfsVec1[1] = float32(freqs[docIdx01].Frequency), float32(freqs[docIdx02].Frequency)
				tfsVec1[2], tfsVec1[3] = float32(freqs[docIdx03].Frequency), float32(freqs[docIdx04].Frequency)
				tfsVec1[4], tfsVec1[5] = float32(freqs[docIdx05].Frequency), float32(freqs[docIdx06].Frequency)
				tfsVec1[6], tfsVec1[7] = float32(freqs[docIdx07].Frequency), float32(freqs[docIdx08].Frequency)
				tfsVec2[0], tfsVec2[1] = float32(freqs[docIdx09].Frequency), float32(freqs[docIdx10].Frequency)
				tfsVec2[2], tfsVec2[3] = float32(freqs[docIdx11].Frequency), float32(freqs[docIdx12].Frequency)
				tfsVec2[4], tfsVec2[5] = float32(freqs[docIdx13].Frequency), float32(freqs[docIdx14].Frequency)
				tfsVec2[6], tfsVec2[7] = float32(freqs[docIdx15].Frequency), float32(freqs[docIdx16].Frequency)
				tfsVec3[0], tfsVec3[1] = float32(freqs[docIdx17].Frequency), float32(freqs[docIdx18].Frequency)
				tfsVec3[2], tfsVec3[3] = float32(freqs[docIdx19].Frequency), float32(freqs[docIdx20].Frequency)
				tfsVec3[4], tfsVec3[5] = float32(freqs[docIdx21].Frequency), float32(freqs[docIdx22].Frequency)
				tfsVec3[6], tfsVec3[7] = float32(freqs[docIdx23].Frequency), float32(freqs[docIdx24].Frequency)
				tfsVec4[0], tfsVec4[1] = float32(freqs[docIdx25].Frequency), float32(freqs[docIdx26].Frequency)
				tfsVec4[2], tfsVec4[3] = float32(freqs[docIdx27].Frequency), float32(freqs[docIdx28].Frequency)
				tfsVec4[4], tfsVec4[5] = float32(freqs[docIdx29].Frequency), float32(freqs[docIdx30].Frequency)
				tfsVec4[6], tfsVec4[7] = float32(freqs[docIdx31].Frequency), float32(freqs[docIdx32].Frequency)
				tfsVec5[0], tfsVec5[1] = float32(freqs[docIdx33].Frequency), float32(freqs[docIdx34].Frequency)
				tfsVec5[2], tfsVec5[3] = float32(freqs[docIdx35].Frequency), float32(freqs[docIdx36].Frequency)
				tfsVec5[4], tfsVec5[5] = float32(freqs[docIdx37].Frequency), float32(freqs[docIdx38].Frequency)
				tfsVec5[6], tfsVec5[7] = float32(freqs[docIdx39].Frequency), float32(freqs[docIdx40].Frequency)
				tfsVec6[0], tfsVec6[1] = float32(freqs[docIdx41].Frequency), float32(freqs[docIdx42].Frequency)
				tfsVec6[2], tfsVec6[3] = float32(freqs[docIdx43].Frequency), float32(freqs[docIdx44].Frequency)
				tfsVec6[4], tfsVec6[5] = float32(freqs[docIdx45].Frequency), float32(freqs[docIdx46].Frequency)
				tfsVec6[6], tfsVec6[7] = float32(freqs[docIdx47].Frequency), float32(freqs[docIdx48].Frequency)
				tfsVec7[0], tfsVec7[1] = float32(freqs[docIdx49].Frequency), float32(freqs[docIdx50].Frequency)
				tfsVec7[2], tfsVec7[3] = float32(freqs[docIdx51].Frequency), float32(freqs[docIdx52].Frequency)
				tfsVec7[4], tfsVec7[5] = float32(freqs[docIdx53].Frequency), float32(freqs[docIdx54].Frequency)
				tfsVec7[6], tfsVec7[7] = float32(freqs[docIdx55].Frequency), float32(freqs[docIdx56].Frequency)
				tfsVec8[0], tfsVec8[1] = float32(freqs[docIdx57].Frequency), float32(freqs[docIdx58].Frequency)
				tfsVec8[2], tfsVec8[3] = float32(freqs[docIdx59].Frequency), float32(freqs[docIdx60].Frequency)
				tfsVec8[4], tfsVec8[5] = float32(freqs[docIdx61].Frequency), float32(freqs[docIdx62].Frequency)
				tfsVec8[6], tfsVec8[7] = float32(freqs[docIdx63].Frequency), float32(freqs[docIdx64].Frequency)

				dls1 := archsimd.LoadFloat32x8Array(&dlsVec1)
				tfs1 := archsimd.LoadFloat32x8Array(&tfsVec1)
				partial1 := tfs1.Add(satXoneMinuxLps)
				denominator1 := dls1.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial1)
				reciprocal1 := denominator1.Reciprocal()
				tfNorms1 := tfs1.Mul(reciprocal1)
				scores1 := idfBoosts.Mul(tfNorms1)
				scores1.StoreArray(&scoresOut1)

				dls2 := archsimd.LoadFloat32x8Array(&dlsVec2)
				tfs2 := archsimd.LoadFloat32x8Array(&tfsVec2)
				partial2 := tfs2.Add(satXoneMinuxLps)
				denominator2 := dls2.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial2)
				reciprocal2 := denominator2.Reciprocal()
				tfNorms2 := tfs2.Mul(reciprocal2)
				scores2 := idfBoosts.Mul(tfNorms2)
				scores2.StoreArray(&scoresOut2)

				dls3 := archsimd.LoadFloat32x8Array(&dlsVec3)
				tfs3 := archsimd.LoadFloat32x8Array(&tfsVec3)
				partial3 := tfs3.Add(satXoneMinuxLps)
				denominator3 := dls3.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial3)
				reciprocal3 := denominator3.Reciprocal()
				tfNorms3 := tfs3.Mul(reciprocal3)
				scores3 := idfBoosts.Mul(tfNorms3)
				scores3.StoreArray(&scoresOut3)

				dls4 := archsimd.LoadFloat32x8Array(&dlsVec4)
				tfs4 := archsimd.LoadFloat32x8Array(&tfsVec4)
				partial4 := tfs4.Add(satXoneMinuxLps)
				denominator4 := dls4.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial4)
				reciprocal4 := denominator4.Reciprocal()
				tfNorms4 := tfs4.Mul(reciprocal4)
				scores4 := idfBoosts.Mul(tfNorms4)
				scores4.StoreArray(&scoresOut4)

				dls5 := archsimd.LoadFloat32x8Array(&dlsVec5)
				tfs5 := archsimd.LoadFloat32x8Array(&tfsVec5)
				partial5 := tfs5.Add(satXoneMinuxLps)
				denominator5 := dls5.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial5)
				reciprocal5 := denominator5.Reciprocal()
				tfNorms5 := tfs5.Mul(reciprocal5)
				scores5 := idfBoosts.Mul(tfNorms5)
				scores5.StoreArray(&scoresOut5)

				dls6 := archsimd.LoadFloat32x8Array(&dlsVec6)
				tfs6 := archsimd.LoadFloat32x8Array(&tfsVec6)
				partial6 := tfs6.Add(satXoneMinuxLps)
				denominator6 := dls6.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial6)
				reciprocal6 := denominator6.Reciprocal()
				tfNorms6 := tfs6.Mul(reciprocal6)
				scores6 := idfBoosts.Mul(tfNorms6)
				scores6.StoreArray(&scoresOut6)

				dls7 := archsimd.LoadFloat32x8Array(&dlsVec7)
				tfs7 := archsimd.LoadFloat32x8Array(&tfsVec7)
				partial7 := tfs7.Add(satXoneMinuxLps)
				denominator7 := dls7.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial7)
				reciprocal7 := denominator7.Reciprocal()
				tfNorms7 := tfs7.Mul(reciprocal7)
				scores7 := idfBoosts.Mul(tfNorms7)
				scores7.StoreArray(&scoresOut7)

				dls8 := archsimd.LoadFloat32x8Array(&dlsVec8)
				tfs8 := archsimd.LoadFloat32x8Array(&tfsVec8)
				partial8 := tfs8.Add(satXoneMinuxLps)
				denominator8 := dls8.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial8)
				reciprocal8 := denominator8.Reciprocal()
				tfNorms8 := tfs8.Mul(reciprocal8)
				scores8 := idfBoosts.Mul(tfNorms8)
				scores8.StoreArray(&scoresOut8)

				guess = ctx.Scoring.Add(guess, docIdx01, scoresOut1[0])
				guess = ctx.Scoring.Add(guess, docIdx02, scoresOut1[1])
				guess = ctx.Scoring.Add(guess, docIdx03, scoresOut1[2])
				guess = ctx.Scoring.Add(guess, docIdx04, scoresOut1[3])
				guess = ctx.Scoring.Add(guess, docIdx05, scoresOut1[4])
				guess = ctx.Scoring.Add(guess, docIdx06, scoresOut1[5])
				guess = ctx.Scoring.Add(guess, docIdx07, scoresOut1[6])
				guess = ctx.Scoring.Add(guess, docIdx08, scoresOut1[7])

				guess = ctx.Scoring.Add(guess, docIdx09, scoresOut2[0])
				guess = ctx.Scoring.Add(guess, docIdx10, scoresOut2[1])
				guess = ctx.Scoring.Add(guess, docIdx11, scoresOut2[2])
				guess = ctx.Scoring.Add(guess, docIdx12, scoresOut2[3])
				guess = ctx.Scoring.Add(guess, docIdx13, scoresOut2[4])
				guess = ctx.Scoring.Add(guess, docIdx14, scoresOut2[5])
				guess = ctx.Scoring.Add(guess, docIdx15, scoresOut2[6])
				guess = ctx.Scoring.Add(guess, docIdx16, scoresOut2[7])

				guess = ctx.Scoring.Add(guess, docIdx17, scoresOut3[0])
				guess = ctx.Scoring.Add(guess, docIdx18, scoresOut3[1])
				guess = ctx.Scoring.Add(guess, docIdx19, scoresOut3[2])
				guess = ctx.Scoring.Add(guess, docIdx20, scoresOut3[3])
				guess = ctx.Scoring.Add(guess, docIdx21, scoresOut3[4])
				guess = ctx.Scoring.Add(guess, docIdx22, scoresOut3[5])
				guess = ctx.Scoring.Add(guess, docIdx23, scoresOut3[6])
				guess = ctx.Scoring.Add(guess, docIdx24, scoresOut3[7])

				guess = ctx.Scoring.Add(guess, docIdx25, scoresOut4[0])
				guess = ctx.Scoring.Add(guess, docIdx26, scoresOut4[1])
				guess = ctx.Scoring.Add(guess, docIdx27, scoresOut4[2])
				guess = ctx.Scoring.Add(guess, docIdx28, scoresOut4[3])
				guess = ctx.Scoring.Add(guess, docIdx29, scoresOut4[4])
				guess = ctx.Scoring.Add(guess, docIdx30, scoresOut4[5])
				guess = ctx.Scoring.Add(guess, docIdx31, scoresOut4[6])
				guess = ctx.Scoring.Add(guess, docIdx32, scoresOut4[7])

				guess = ctx.Scoring.Add(guess, docIdx33, scoresOut5[0])
				guess = ctx.Scoring.Add(guess, docIdx34, scoresOut5[1])
				guess = ctx.Scoring.Add(guess, docIdx35, scoresOut5[2])
				guess = ctx.Scoring.Add(guess, docIdx36, scoresOut5[3])
				guess = ctx.Scoring.Add(guess, docIdx37, scoresOut5[4])
				guess = ctx.Scoring.Add(guess, docIdx38, scoresOut5[5])
				guess = ctx.Scoring.Add(guess, docIdx39, scoresOut5[6])
				guess = ctx.Scoring.Add(guess, docIdx40, scoresOut5[7])

				guess = ctx.Scoring.Add(guess, docIdx41, scoresOut6[0])
				guess = ctx.Scoring.Add(guess, docIdx42, scoresOut6[1])
				guess = ctx.Scoring.Add(guess, docIdx43, scoresOut6[2])
				guess = ctx.Scoring.Add(guess, docIdx44, scoresOut6[3])
				guess = ctx.Scoring.Add(guess, docIdx45, scoresOut6[4])
				guess = ctx.Scoring.Add(guess, docIdx46, scoresOut6[5])
				guess = ctx.Scoring.Add(guess, docIdx47, scoresOut6[6])
				guess = ctx.Scoring.Add(guess, docIdx48, scoresOut6[7])

				guess = ctx.Scoring.Add(guess, docIdx49, scoresOut7[0])
				guess = ctx.Scoring.Add(guess, docIdx50, scoresOut7[1])
				guess = ctx.Scoring.Add(guess, docIdx51, scoresOut7[2])
				guess = ctx.Scoring.Add(guess, docIdx52, scoresOut7[3])
				guess = ctx.Scoring.Add(guess, docIdx53, scoresOut7[4])
				guess = ctx.Scoring.Add(guess, docIdx54, scoresOut7[5])
				guess = ctx.Scoring.Add(guess, docIdx55, scoresOut7[6])
				guess = ctx.Scoring.Add(guess, docIdx56, scoresOut7[7])

				guess = ctx.Scoring.Add(guess, docIdx57, scoresOut8[0])
				guess = ctx.Scoring.Add(guess, docIdx58, scoresOut8[1])
				guess = ctx.Scoring.Add(guess, docIdx59, scoresOut8[2])
				guess = ctx.Scoring.Add(guess, docIdx60, scoresOut8[3])
				guess = ctx.Scoring.Add(guess, docIdx61, scoresOut8[4])
				guess = ctx.Scoring.Add(guess, docIdx62, scoresOut8[5])
				guess = ctx.Scoring.Add(guess, docIdx63, scoresOut8[6])
				guess = ctx.Scoring.Add(guess, docIdx64, scoresOut8[7])
			}

			// Remainder that still fills one whole vector.
			for i := n64; i < n8; i += LaneCountAvx2 {
				docIdx01, docIdx02, docIdx03, docIdx04 := resolved[0+i], resolved[1+i], resolved[2+i], resolved[3+i]
				docIdx05, docIdx06, docIdx07, docIdx08 := resolved[4+i], resolved[5+i], resolved[6+i], resolved[7+i]

				// Dense document lengths: index directly, no probe needed.
				dlsVec1[0], dlsVec1[1] = float32(docLengths[docIdx01].Length), float32(docLengths[docIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docIdx03].Length), float32(docLengths[docIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docIdx05].Length), float32(docLengths[docIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docIdx07].Length), float32(docLengths[docIdx08].Length)

				// Dense token frequencies: index directly, no probe needed.
				tfsVec1[0], tfsVec1[1] = float32(freqs[docIdx01].Frequency), float32(freqs[docIdx02].Frequency)
				tfsVec1[2], tfsVec1[3] = float32(freqs[docIdx03].Frequency), float32(freqs[docIdx04].Frequency)
				tfsVec1[4], tfsVec1[5] = float32(freqs[docIdx05].Frequency), float32(freqs[docIdx06].Frequency)
				tfsVec1[6], tfsVec1[7] = float32(freqs[docIdx07].Frequency), float32(freqs[docIdx08].Frequency)

				dls1 := archsimd.LoadFloat32x8Array(&dlsVec1)
				tfs1 := archsimd.LoadFloat32x8Array(&tfsVec1)
				partial1 := tfs1.Add(satXoneMinuxLps)
				denominator1 := dls1.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial1)
				reciprocal1 := denominator1.Reciprocal()
				tfNorms1 := tfs1.Mul(reciprocal1)
				scores1 := idfBoosts.Mul(tfNorms1)
				scores1.StoreArray(&scoresOut1)

				guess = ctx.Scoring.Add(guess, docIdx01, scoresOut1[0])
				guess = ctx.Scoring.Add(guess, docIdx02, scoresOut1[1])
				guess = ctx.Scoring.Add(guess, docIdx03, scoresOut1[2])
				guess = ctx.Scoring.Add(guess, docIdx04, scoresOut1[3])
				guess = ctx.Scoring.Add(guess, docIdx05, scoresOut1[4])
				guess = ctx.Scoring.Add(guess, docIdx06, scoresOut1[5])
				guess = ctx.Scoring.Add(guess, docIdx07, scoresOut1[6])
				guess = ctx.Scoring.Add(guess, docIdx08, scoresOut1[7])
			}

			// Final < 8 documents, scalar.
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
			// 64 documents per iteration: 8 x Float32x8.
			for i := 0; i < n64; i += UnrollingFactorAvx2 {
				docIdx01, docIdx02, docIdx03, docIdx04 := resolved[0+i], resolved[1+i], resolved[2+i], resolved[3+i]
				docIdx05, docIdx06, docIdx07, docIdx08 := resolved[4+i], resolved[5+i], resolved[6+i], resolved[7+i]
				docIdx09, docIdx10, docIdx11, docIdx12 := resolved[8+i], resolved[9+i], resolved[10+i], resolved[11+i]
				docIdx13, docIdx14, docIdx15, docIdx16 := resolved[12+i], resolved[13+i], resolved[14+i], resolved[15+i]
				docIdx17, docIdx18, docIdx19, docIdx20 := resolved[16+i], resolved[17+i], resolved[18+i], resolved[19+i]
				docIdx21, docIdx22, docIdx23, docIdx24 := resolved[20+i], resolved[21+i], resolved[22+i], resolved[23+i]
				docIdx25, docIdx26, docIdx27, docIdx28 := resolved[24+i], resolved[25+i], resolved[26+i], resolved[27+i]
				docIdx29, docIdx30, docIdx31, docIdx32 := resolved[28+i], resolved[29+i], resolved[30+i], resolved[31+i]
				docIdx33, docIdx34, docIdx35, docIdx36 := resolved[32+i], resolved[33+i], resolved[34+i], resolved[35+i]
				docIdx37, docIdx38, docIdx39, docIdx40 := resolved[36+i], resolved[37+i], resolved[38+i], resolved[39+i]
				docIdx41, docIdx42, docIdx43, docIdx44 := resolved[40+i], resolved[41+i], resolved[42+i], resolved[43+i]
				docIdx45, docIdx46, docIdx47, docIdx48 := resolved[44+i], resolved[45+i], resolved[46+i], resolved[47+i]
				docIdx49, docIdx50, docIdx51, docIdx52 := resolved[48+i], resolved[49+i], resolved[50+i], resolved[51+i]
				docIdx53, docIdx54, docIdx55, docIdx56 := resolved[52+i], resolved[53+i], resolved[54+i], resolved[55+i]
				docIdx57, docIdx58, docIdx59, docIdx60 := resolved[56+i], resolved[57+i], resolved[58+i], resolved[59+i]
				docIdx61, docIdx62, docIdx63, docIdx64 := resolved[60+i], resolved[61+i], resolved[62+i], resolved[63+i]

				// Sparse document lengths: descending cascade, each probe narrows the next.
				docLengthIdx64, _ := slices.BinarySearchFunc(docLengths, docIdx64, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx63, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx64], docIdx63, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx62, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx63], docIdx62, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx61, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx62], docIdx61, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx60, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx61], docIdx60, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx59, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx60], docIdx59, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx58, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx59], docIdx58, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx57, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx58], docIdx57, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx56, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx57], docIdx56, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx55, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx56], docIdx55, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx54, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx55], docIdx54, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx53, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx54], docIdx53, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx52, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx53], docIdx52, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx51, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx52], docIdx51, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx50, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx51], docIdx50, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx49, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx50], docIdx49, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx48, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx49], docIdx48, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx47, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx48], docIdx47, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx46, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx47], docIdx46, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx45, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx46], docIdx45, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx44, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx45], docIdx44, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx43, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx44], docIdx43, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx42, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx43], docIdx42, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx41, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx42], docIdx41, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx40, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx41], docIdx40, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx39, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx40], docIdx39, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx38, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx39], docIdx38, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx37, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx38], docIdx37, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx36, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx37], docIdx36, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx35, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx36], docIdx35, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx34, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx35], docIdx34, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx33, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx34], docIdx33, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx32, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx33], docIdx32, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx31, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx32], docIdx31, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx30, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx31], docIdx30, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx29, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx30], docIdx29, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx28, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx29], docIdx28, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx27, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx28], docIdx27, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx26, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx27], docIdx26, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx25, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx26], docIdx25, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx24, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx25], docIdx24, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx23, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx24], docIdx23, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx22, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx23], docIdx22, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx21, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx22], docIdx21, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx20, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx21], docIdx20, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx19, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx20], docIdx19, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx18, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx19], docIdx18, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx17, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx18], docIdx17, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx16, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx17], docIdx16, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx15, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx16], docIdx15, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx14, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx15], docIdx14, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx13, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx14], docIdx13, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx12, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx13], docIdx12, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx11, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx12], docIdx11, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx10, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx11], docIdx10, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx09, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx10], docIdx09, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx08, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx09], docIdx08, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx07, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx08], docIdx07, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx06, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx07], docIdx06, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx05, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx06], docIdx05, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx04, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx05], docIdx04, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx03, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx04], docIdx03, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx02, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx03], docIdx02, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx01, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx02], docIdx01, CmpDocumentLengthEntryAndDocumentIndex)

				dlsVec1[0], dlsVec1[1] = float32(docLengths[docLengthIdx01].Length), float32(docLengths[docLengthIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docLengthIdx03].Length), float32(docLengths[docLengthIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docLengthIdx05].Length), float32(docLengths[docLengthIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docLengthIdx07].Length), float32(docLengths[docLengthIdx08].Length)
				dlsVec2[0], dlsVec2[1] = float32(docLengths[docLengthIdx09].Length), float32(docLengths[docLengthIdx10].Length)
				dlsVec2[2], dlsVec2[3] = float32(docLengths[docLengthIdx11].Length), float32(docLengths[docLengthIdx12].Length)
				dlsVec2[4], dlsVec2[5] = float32(docLengths[docLengthIdx13].Length), float32(docLengths[docLengthIdx14].Length)
				dlsVec2[6], dlsVec2[7] = float32(docLengths[docLengthIdx15].Length), float32(docLengths[docLengthIdx16].Length)
				dlsVec3[0], dlsVec3[1] = float32(docLengths[docLengthIdx17].Length), float32(docLengths[docLengthIdx18].Length)
				dlsVec3[2], dlsVec3[3] = float32(docLengths[docLengthIdx19].Length), float32(docLengths[docLengthIdx20].Length)
				dlsVec3[4], dlsVec3[5] = float32(docLengths[docLengthIdx21].Length), float32(docLengths[docLengthIdx22].Length)
				dlsVec3[6], dlsVec3[7] = float32(docLengths[docLengthIdx23].Length), float32(docLengths[docLengthIdx24].Length)
				dlsVec4[0], dlsVec4[1] = float32(docLengths[docLengthIdx25].Length), float32(docLengths[docLengthIdx26].Length)
				dlsVec4[2], dlsVec4[3] = float32(docLengths[docLengthIdx27].Length), float32(docLengths[docLengthIdx28].Length)
				dlsVec4[4], dlsVec4[5] = float32(docLengths[docLengthIdx29].Length), float32(docLengths[docLengthIdx30].Length)
				dlsVec4[6], dlsVec4[7] = float32(docLengths[docLengthIdx31].Length), float32(docLengths[docLengthIdx32].Length)
				dlsVec5[0], dlsVec5[1] = float32(docLengths[docLengthIdx33].Length), float32(docLengths[docLengthIdx34].Length)
				dlsVec5[2], dlsVec5[3] = float32(docLengths[docLengthIdx35].Length), float32(docLengths[docLengthIdx36].Length)
				dlsVec5[4], dlsVec5[5] = float32(docLengths[docLengthIdx37].Length), float32(docLengths[docLengthIdx38].Length)
				dlsVec5[6], dlsVec5[7] = float32(docLengths[docLengthIdx39].Length), float32(docLengths[docLengthIdx40].Length)
				dlsVec6[0], dlsVec6[1] = float32(docLengths[docLengthIdx41].Length), float32(docLengths[docLengthIdx42].Length)
				dlsVec6[2], dlsVec6[3] = float32(docLengths[docLengthIdx43].Length), float32(docLengths[docLengthIdx44].Length)
				dlsVec6[4], dlsVec6[5] = float32(docLengths[docLengthIdx45].Length), float32(docLengths[docLengthIdx46].Length)
				dlsVec6[6], dlsVec6[7] = float32(docLengths[docLengthIdx47].Length), float32(docLengths[docLengthIdx48].Length)
				dlsVec7[0], dlsVec7[1] = float32(docLengths[docLengthIdx49].Length), float32(docLengths[docLengthIdx50].Length)
				dlsVec7[2], dlsVec7[3] = float32(docLengths[docLengthIdx51].Length), float32(docLengths[docLengthIdx52].Length)
				dlsVec7[4], dlsVec7[5] = float32(docLengths[docLengthIdx53].Length), float32(docLengths[docLengthIdx54].Length)
				dlsVec7[6], dlsVec7[7] = float32(docLengths[docLengthIdx55].Length), float32(docLengths[docLengthIdx56].Length)
				dlsVec8[0], dlsVec8[1] = float32(docLengths[docLengthIdx57].Length), float32(docLengths[docLengthIdx58].Length)
				dlsVec8[2], dlsVec8[3] = float32(docLengths[docLengthIdx59].Length), float32(docLengths[docLengthIdx60].Length)
				dlsVec8[4], dlsVec8[5] = float32(docLengths[docLengthIdx61].Length), float32(docLengths[docLengthIdx62].Length)
				dlsVec8[6], dlsVec8[7] = float32(docLengths[docLengthIdx63].Length), float32(docLengths[docLengthIdx64].Length)
				docLengths = docLengths[1+docLengthIdx64:]

				// Dense token frequencies: index directly, no probe needed.
				tfsVec1[0], tfsVec1[1] = float32(freqs[docIdx01].Frequency), float32(freqs[docIdx02].Frequency)
				tfsVec1[2], tfsVec1[3] = float32(freqs[docIdx03].Frequency), float32(freqs[docIdx04].Frequency)
				tfsVec1[4], tfsVec1[5] = float32(freqs[docIdx05].Frequency), float32(freqs[docIdx06].Frequency)
				tfsVec1[6], tfsVec1[7] = float32(freqs[docIdx07].Frequency), float32(freqs[docIdx08].Frequency)
				tfsVec2[0], tfsVec2[1] = float32(freqs[docIdx09].Frequency), float32(freqs[docIdx10].Frequency)
				tfsVec2[2], tfsVec2[3] = float32(freqs[docIdx11].Frequency), float32(freqs[docIdx12].Frequency)
				tfsVec2[4], tfsVec2[5] = float32(freqs[docIdx13].Frequency), float32(freqs[docIdx14].Frequency)
				tfsVec2[6], tfsVec2[7] = float32(freqs[docIdx15].Frequency), float32(freqs[docIdx16].Frequency)
				tfsVec3[0], tfsVec3[1] = float32(freqs[docIdx17].Frequency), float32(freqs[docIdx18].Frequency)
				tfsVec3[2], tfsVec3[3] = float32(freqs[docIdx19].Frequency), float32(freqs[docIdx20].Frequency)
				tfsVec3[4], tfsVec3[5] = float32(freqs[docIdx21].Frequency), float32(freqs[docIdx22].Frequency)
				tfsVec3[6], tfsVec3[7] = float32(freqs[docIdx23].Frequency), float32(freqs[docIdx24].Frequency)
				tfsVec4[0], tfsVec4[1] = float32(freqs[docIdx25].Frequency), float32(freqs[docIdx26].Frequency)
				tfsVec4[2], tfsVec4[3] = float32(freqs[docIdx27].Frequency), float32(freqs[docIdx28].Frequency)
				tfsVec4[4], tfsVec4[5] = float32(freqs[docIdx29].Frequency), float32(freqs[docIdx30].Frequency)
				tfsVec4[6], tfsVec4[7] = float32(freqs[docIdx31].Frequency), float32(freqs[docIdx32].Frequency)
				tfsVec5[0], tfsVec5[1] = float32(freqs[docIdx33].Frequency), float32(freqs[docIdx34].Frequency)
				tfsVec5[2], tfsVec5[3] = float32(freqs[docIdx35].Frequency), float32(freqs[docIdx36].Frequency)
				tfsVec5[4], tfsVec5[5] = float32(freqs[docIdx37].Frequency), float32(freqs[docIdx38].Frequency)
				tfsVec5[6], tfsVec5[7] = float32(freqs[docIdx39].Frequency), float32(freqs[docIdx40].Frequency)
				tfsVec6[0], tfsVec6[1] = float32(freqs[docIdx41].Frequency), float32(freqs[docIdx42].Frequency)
				tfsVec6[2], tfsVec6[3] = float32(freqs[docIdx43].Frequency), float32(freqs[docIdx44].Frequency)
				tfsVec6[4], tfsVec6[5] = float32(freqs[docIdx45].Frequency), float32(freqs[docIdx46].Frequency)
				tfsVec6[6], tfsVec6[7] = float32(freqs[docIdx47].Frequency), float32(freqs[docIdx48].Frequency)
				tfsVec7[0], tfsVec7[1] = float32(freqs[docIdx49].Frequency), float32(freqs[docIdx50].Frequency)
				tfsVec7[2], tfsVec7[3] = float32(freqs[docIdx51].Frequency), float32(freqs[docIdx52].Frequency)
				tfsVec7[4], tfsVec7[5] = float32(freqs[docIdx53].Frequency), float32(freqs[docIdx54].Frequency)
				tfsVec7[6], tfsVec7[7] = float32(freqs[docIdx55].Frequency), float32(freqs[docIdx56].Frequency)
				tfsVec8[0], tfsVec8[1] = float32(freqs[docIdx57].Frequency), float32(freqs[docIdx58].Frequency)
				tfsVec8[2], tfsVec8[3] = float32(freqs[docIdx59].Frequency), float32(freqs[docIdx60].Frequency)
				tfsVec8[4], tfsVec8[5] = float32(freqs[docIdx61].Frequency), float32(freqs[docIdx62].Frequency)
				tfsVec8[6], tfsVec8[7] = float32(freqs[docIdx63].Frequency), float32(freqs[docIdx64].Frequency)

				dls1 := archsimd.LoadFloat32x8Array(&dlsVec1)
				tfs1 := archsimd.LoadFloat32x8Array(&tfsVec1)
				partial1 := tfs1.Add(satXoneMinuxLps)
				denominator1 := dls1.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial1)
				reciprocal1 := denominator1.Reciprocal()
				tfNorms1 := tfs1.Mul(reciprocal1)
				scores1 := idfBoosts.Mul(tfNorms1)
				scores1.StoreArray(&scoresOut1)

				dls2 := archsimd.LoadFloat32x8Array(&dlsVec2)
				tfs2 := archsimd.LoadFloat32x8Array(&tfsVec2)
				partial2 := tfs2.Add(satXoneMinuxLps)
				denominator2 := dls2.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial2)
				reciprocal2 := denominator2.Reciprocal()
				tfNorms2 := tfs2.Mul(reciprocal2)
				scores2 := idfBoosts.Mul(tfNorms2)
				scores2.StoreArray(&scoresOut2)

				dls3 := archsimd.LoadFloat32x8Array(&dlsVec3)
				tfs3 := archsimd.LoadFloat32x8Array(&tfsVec3)
				partial3 := tfs3.Add(satXoneMinuxLps)
				denominator3 := dls3.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial3)
				reciprocal3 := denominator3.Reciprocal()
				tfNorms3 := tfs3.Mul(reciprocal3)
				scores3 := idfBoosts.Mul(tfNorms3)
				scores3.StoreArray(&scoresOut3)

				dls4 := archsimd.LoadFloat32x8Array(&dlsVec4)
				tfs4 := archsimd.LoadFloat32x8Array(&tfsVec4)
				partial4 := tfs4.Add(satXoneMinuxLps)
				denominator4 := dls4.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial4)
				reciprocal4 := denominator4.Reciprocal()
				tfNorms4 := tfs4.Mul(reciprocal4)
				scores4 := idfBoosts.Mul(tfNorms4)
				scores4.StoreArray(&scoresOut4)

				dls5 := archsimd.LoadFloat32x8Array(&dlsVec5)
				tfs5 := archsimd.LoadFloat32x8Array(&tfsVec5)
				partial5 := tfs5.Add(satXoneMinuxLps)
				denominator5 := dls5.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial5)
				reciprocal5 := denominator5.Reciprocal()
				tfNorms5 := tfs5.Mul(reciprocal5)
				scores5 := idfBoosts.Mul(tfNorms5)
				scores5.StoreArray(&scoresOut5)

				dls6 := archsimd.LoadFloat32x8Array(&dlsVec6)
				tfs6 := archsimd.LoadFloat32x8Array(&tfsVec6)
				partial6 := tfs6.Add(satXoneMinuxLps)
				denominator6 := dls6.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial6)
				reciprocal6 := denominator6.Reciprocal()
				tfNorms6 := tfs6.Mul(reciprocal6)
				scores6 := idfBoosts.Mul(tfNorms6)
				scores6.StoreArray(&scoresOut6)

				dls7 := archsimd.LoadFloat32x8Array(&dlsVec7)
				tfs7 := archsimd.LoadFloat32x8Array(&tfsVec7)
				partial7 := tfs7.Add(satXoneMinuxLps)
				denominator7 := dls7.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial7)
				reciprocal7 := denominator7.Reciprocal()
				tfNorms7 := tfs7.Mul(reciprocal7)
				scores7 := idfBoosts.Mul(tfNorms7)
				scores7.StoreArray(&scoresOut7)

				dls8 := archsimd.LoadFloat32x8Array(&dlsVec8)
				tfs8 := archsimd.LoadFloat32x8Array(&tfsVec8)
				partial8 := tfs8.Add(satXoneMinuxLps)
				denominator8 := dls8.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial8)
				reciprocal8 := denominator8.Reciprocal()
				tfNorms8 := tfs8.Mul(reciprocal8)
				scores8 := idfBoosts.Mul(tfNorms8)
				scores8.StoreArray(&scoresOut8)

				guess = ctx.Scoring.Add(guess, docIdx01, scoresOut1[0])
				guess = ctx.Scoring.Add(guess, docIdx02, scoresOut1[1])
				guess = ctx.Scoring.Add(guess, docIdx03, scoresOut1[2])
				guess = ctx.Scoring.Add(guess, docIdx04, scoresOut1[3])
				guess = ctx.Scoring.Add(guess, docIdx05, scoresOut1[4])
				guess = ctx.Scoring.Add(guess, docIdx06, scoresOut1[5])
				guess = ctx.Scoring.Add(guess, docIdx07, scoresOut1[6])
				guess = ctx.Scoring.Add(guess, docIdx08, scoresOut1[7])

				guess = ctx.Scoring.Add(guess, docIdx09, scoresOut2[0])
				guess = ctx.Scoring.Add(guess, docIdx10, scoresOut2[1])
				guess = ctx.Scoring.Add(guess, docIdx11, scoresOut2[2])
				guess = ctx.Scoring.Add(guess, docIdx12, scoresOut2[3])
				guess = ctx.Scoring.Add(guess, docIdx13, scoresOut2[4])
				guess = ctx.Scoring.Add(guess, docIdx14, scoresOut2[5])
				guess = ctx.Scoring.Add(guess, docIdx15, scoresOut2[6])
				guess = ctx.Scoring.Add(guess, docIdx16, scoresOut2[7])

				guess = ctx.Scoring.Add(guess, docIdx17, scoresOut3[0])
				guess = ctx.Scoring.Add(guess, docIdx18, scoresOut3[1])
				guess = ctx.Scoring.Add(guess, docIdx19, scoresOut3[2])
				guess = ctx.Scoring.Add(guess, docIdx20, scoresOut3[3])
				guess = ctx.Scoring.Add(guess, docIdx21, scoresOut3[4])
				guess = ctx.Scoring.Add(guess, docIdx22, scoresOut3[5])
				guess = ctx.Scoring.Add(guess, docIdx23, scoresOut3[6])
				guess = ctx.Scoring.Add(guess, docIdx24, scoresOut3[7])

				guess = ctx.Scoring.Add(guess, docIdx25, scoresOut4[0])
				guess = ctx.Scoring.Add(guess, docIdx26, scoresOut4[1])
				guess = ctx.Scoring.Add(guess, docIdx27, scoresOut4[2])
				guess = ctx.Scoring.Add(guess, docIdx28, scoresOut4[3])
				guess = ctx.Scoring.Add(guess, docIdx29, scoresOut4[4])
				guess = ctx.Scoring.Add(guess, docIdx30, scoresOut4[5])
				guess = ctx.Scoring.Add(guess, docIdx31, scoresOut4[6])
				guess = ctx.Scoring.Add(guess, docIdx32, scoresOut4[7])

				guess = ctx.Scoring.Add(guess, docIdx33, scoresOut5[0])
				guess = ctx.Scoring.Add(guess, docIdx34, scoresOut5[1])
				guess = ctx.Scoring.Add(guess, docIdx35, scoresOut5[2])
				guess = ctx.Scoring.Add(guess, docIdx36, scoresOut5[3])
				guess = ctx.Scoring.Add(guess, docIdx37, scoresOut5[4])
				guess = ctx.Scoring.Add(guess, docIdx38, scoresOut5[5])
				guess = ctx.Scoring.Add(guess, docIdx39, scoresOut5[6])
				guess = ctx.Scoring.Add(guess, docIdx40, scoresOut5[7])

				guess = ctx.Scoring.Add(guess, docIdx41, scoresOut6[0])
				guess = ctx.Scoring.Add(guess, docIdx42, scoresOut6[1])
				guess = ctx.Scoring.Add(guess, docIdx43, scoresOut6[2])
				guess = ctx.Scoring.Add(guess, docIdx44, scoresOut6[3])
				guess = ctx.Scoring.Add(guess, docIdx45, scoresOut6[4])
				guess = ctx.Scoring.Add(guess, docIdx46, scoresOut6[5])
				guess = ctx.Scoring.Add(guess, docIdx47, scoresOut6[6])
				guess = ctx.Scoring.Add(guess, docIdx48, scoresOut6[7])

				guess = ctx.Scoring.Add(guess, docIdx49, scoresOut7[0])
				guess = ctx.Scoring.Add(guess, docIdx50, scoresOut7[1])
				guess = ctx.Scoring.Add(guess, docIdx51, scoresOut7[2])
				guess = ctx.Scoring.Add(guess, docIdx52, scoresOut7[3])
				guess = ctx.Scoring.Add(guess, docIdx53, scoresOut7[4])
				guess = ctx.Scoring.Add(guess, docIdx54, scoresOut7[5])
				guess = ctx.Scoring.Add(guess, docIdx55, scoresOut7[6])
				guess = ctx.Scoring.Add(guess, docIdx56, scoresOut7[7])

				guess = ctx.Scoring.Add(guess, docIdx57, scoresOut8[0])
				guess = ctx.Scoring.Add(guess, docIdx58, scoresOut8[1])
				guess = ctx.Scoring.Add(guess, docIdx59, scoresOut8[2])
				guess = ctx.Scoring.Add(guess, docIdx60, scoresOut8[3])
				guess = ctx.Scoring.Add(guess, docIdx61, scoresOut8[4])
				guess = ctx.Scoring.Add(guess, docIdx62, scoresOut8[5])
				guess = ctx.Scoring.Add(guess, docIdx63, scoresOut8[6])
				guess = ctx.Scoring.Add(guess, docIdx64, scoresOut8[7])
			}

			// Remainder that still fills one whole vector.
			for i := n64; i < n8; i += LaneCountAvx2 {
				docIdx01, docIdx02, docIdx03, docIdx04 := resolved[0+i], resolved[1+i], resolved[2+i], resolved[3+i]
				docIdx05, docIdx06, docIdx07, docIdx08 := resolved[4+i], resolved[5+i], resolved[6+i], resolved[7+i]

				// Sparse document lengths: descending cascade, each probe narrows the next.
				docLengthIdx08, _ := slices.BinarySearchFunc(docLengths, docIdx08, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx07, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx08], docIdx07, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx06, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx07], docIdx06, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx05, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx06], docIdx05, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx04, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx05], docIdx04, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx03, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx04], docIdx03, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx02, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx03], docIdx02, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx01, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx02], docIdx01, CmpDocumentLengthEntryAndDocumentIndex)

				dlsVec1[0], dlsVec1[1] = float32(docLengths[docLengthIdx01].Length), float32(docLengths[docLengthIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docLengthIdx03].Length), float32(docLengths[docLengthIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docLengthIdx05].Length), float32(docLengths[docLengthIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docLengthIdx07].Length), float32(docLengths[docLengthIdx08].Length)
				docLengths = docLengths[1+docLengthIdx08:]

				// Dense token frequencies: index directly, no probe needed.
				tfsVec1[0], tfsVec1[1] = float32(freqs[docIdx01].Frequency), float32(freqs[docIdx02].Frequency)
				tfsVec1[2], tfsVec1[3] = float32(freqs[docIdx03].Frequency), float32(freqs[docIdx04].Frequency)
				tfsVec1[4], tfsVec1[5] = float32(freqs[docIdx05].Frequency), float32(freqs[docIdx06].Frequency)
				tfsVec1[6], tfsVec1[7] = float32(freqs[docIdx07].Frequency), float32(freqs[docIdx08].Frequency)

				dls1 := archsimd.LoadFloat32x8Array(&dlsVec1)
				tfs1 := archsimd.LoadFloat32x8Array(&tfsVec1)
				partial1 := tfs1.Add(satXoneMinuxLps)
				denominator1 := dls1.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial1)
				reciprocal1 := denominator1.Reciprocal()
				tfNorms1 := tfs1.Mul(reciprocal1)
				scores1 := idfBoosts.Mul(tfNorms1)
				scores1.StoreArray(&scoresOut1)

				guess = ctx.Scoring.Add(guess, docIdx01, scoresOut1[0])
				guess = ctx.Scoring.Add(guess, docIdx02, scoresOut1[1])
				guess = ctx.Scoring.Add(guess, docIdx03, scoresOut1[2])
				guess = ctx.Scoring.Add(guess, docIdx04, scoresOut1[3])
				guess = ctx.Scoring.Add(guess, docIdx05, scoresOut1[4])
				guess = ctx.Scoring.Add(guess, docIdx06, scoresOut1[5])
				guess = ctx.Scoring.Add(guess, docIdx07, scoresOut1[6])
				guess = ctx.Scoring.Add(guess, docIdx08, scoresOut1[7])
			}

			// Final < 8 documents, scalar.
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
			// 64 documents per iteration: 8 x Float32x8.
			for i := 0; i < n64; i += UnrollingFactorAvx2 {
				docIdx01, docIdx02, docIdx03, docIdx04 := resolved[0+i], resolved[1+i], resolved[2+i], resolved[3+i]
				docIdx05, docIdx06, docIdx07, docIdx08 := resolved[4+i], resolved[5+i], resolved[6+i], resolved[7+i]
				docIdx09, docIdx10, docIdx11, docIdx12 := resolved[8+i], resolved[9+i], resolved[10+i], resolved[11+i]
				docIdx13, docIdx14, docIdx15, docIdx16 := resolved[12+i], resolved[13+i], resolved[14+i], resolved[15+i]
				docIdx17, docIdx18, docIdx19, docIdx20 := resolved[16+i], resolved[17+i], resolved[18+i], resolved[19+i]
				docIdx21, docIdx22, docIdx23, docIdx24 := resolved[20+i], resolved[21+i], resolved[22+i], resolved[23+i]
				docIdx25, docIdx26, docIdx27, docIdx28 := resolved[24+i], resolved[25+i], resolved[26+i], resolved[27+i]
				docIdx29, docIdx30, docIdx31, docIdx32 := resolved[28+i], resolved[29+i], resolved[30+i], resolved[31+i]
				docIdx33, docIdx34, docIdx35, docIdx36 := resolved[32+i], resolved[33+i], resolved[34+i], resolved[35+i]
				docIdx37, docIdx38, docIdx39, docIdx40 := resolved[36+i], resolved[37+i], resolved[38+i], resolved[39+i]
				docIdx41, docIdx42, docIdx43, docIdx44 := resolved[40+i], resolved[41+i], resolved[42+i], resolved[43+i]
				docIdx45, docIdx46, docIdx47, docIdx48 := resolved[44+i], resolved[45+i], resolved[46+i], resolved[47+i]
				docIdx49, docIdx50, docIdx51, docIdx52 := resolved[48+i], resolved[49+i], resolved[50+i], resolved[51+i]
				docIdx53, docIdx54, docIdx55, docIdx56 := resolved[52+i], resolved[53+i], resolved[54+i], resolved[55+i]
				docIdx57, docIdx58, docIdx59, docIdx60 := resolved[56+i], resolved[57+i], resolved[58+i], resolved[59+i]
				docIdx61, docIdx62, docIdx63, docIdx64 := resolved[60+i], resolved[61+i], resolved[62+i], resolved[63+i]

				// Dense document lengths: index directly, no probe needed.
				dlsVec1[0], dlsVec1[1] = float32(docLengths[docIdx01].Length), float32(docLengths[docIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docIdx03].Length), float32(docLengths[docIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docIdx05].Length), float32(docLengths[docIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docIdx07].Length), float32(docLengths[docIdx08].Length)
				dlsVec2[0], dlsVec2[1] = float32(docLengths[docIdx09].Length), float32(docLengths[docIdx10].Length)
				dlsVec2[2], dlsVec2[3] = float32(docLengths[docIdx11].Length), float32(docLengths[docIdx12].Length)
				dlsVec2[4], dlsVec2[5] = float32(docLengths[docIdx13].Length), float32(docLengths[docIdx14].Length)
				dlsVec2[6], dlsVec2[7] = float32(docLengths[docIdx15].Length), float32(docLengths[docIdx16].Length)
				dlsVec3[0], dlsVec3[1] = float32(docLengths[docIdx17].Length), float32(docLengths[docIdx18].Length)
				dlsVec3[2], dlsVec3[3] = float32(docLengths[docIdx19].Length), float32(docLengths[docIdx20].Length)
				dlsVec3[4], dlsVec3[5] = float32(docLengths[docIdx21].Length), float32(docLengths[docIdx22].Length)
				dlsVec3[6], dlsVec3[7] = float32(docLengths[docIdx23].Length), float32(docLengths[docIdx24].Length)
				dlsVec4[0], dlsVec4[1] = float32(docLengths[docIdx25].Length), float32(docLengths[docIdx26].Length)
				dlsVec4[2], dlsVec4[3] = float32(docLengths[docIdx27].Length), float32(docLengths[docIdx28].Length)
				dlsVec4[4], dlsVec4[5] = float32(docLengths[docIdx29].Length), float32(docLengths[docIdx30].Length)
				dlsVec4[6], dlsVec4[7] = float32(docLengths[docIdx31].Length), float32(docLengths[docIdx32].Length)
				dlsVec5[0], dlsVec5[1] = float32(docLengths[docIdx33].Length), float32(docLengths[docIdx34].Length)
				dlsVec5[2], dlsVec5[3] = float32(docLengths[docIdx35].Length), float32(docLengths[docIdx36].Length)
				dlsVec5[4], dlsVec5[5] = float32(docLengths[docIdx37].Length), float32(docLengths[docIdx38].Length)
				dlsVec5[6], dlsVec5[7] = float32(docLengths[docIdx39].Length), float32(docLengths[docIdx40].Length)
				dlsVec6[0], dlsVec6[1] = float32(docLengths[docIdx41].Length), float32(docLengths[docIdx42].Length)
				dlsVec6[2], dlsVec6[3] = float32(docLengths[docIdx43].Length), float32(docLengths[docIdx44].Length)
				dlsVec6[4], dlsVec6[5] = float32(docLengths[docIdx45].Length), float32(docLengths[docIdx46].Length)
				dlsVec6[6], dlsVec6[7] = float32(docLengths[docIdx47].Length), float32(docLengths[docIdx48].Length)
				dlsVec7[0], dlsVec7[1] = float32(docLengths[docIdx49].Length), float32(docLengths[docIdx50].Length)
				dlsVec7[2], dlsVec7[3] = float32(docLengths[docIdx51].Length), float32(docLengths[docIdx52].Length)
				dlsVec7[4], dlsVec7[5] = float32(docLengths[docIdx53].Length), float32(docLengths[docIdx54].Length)
				dlsVec7[6], dlsVec7[7] = float32(docLengths[docIdx55].Length), float32(docLengths[docIdx56].Length)
				dlsVec8[0], dlsVec8[1] = float32(docLengths[docIdx57].Length), float32(docLengths[docIdx58].Length)
				dlsVec8[2], dlsVec8[3] = float32(docLengths[docIdx59].Length), float32(docLengths[docIdx60].Length)
				dlsVec8[4], dlsVec8[5] = float32(docLengths[docIdx61].Length), float32(docLengths[docIdx62].Length)
				dlsVec8[6], dlsVec8[7] = float32(docLengths[docIdx63].Length), float32(docLengths[docIdx64].Length)

				// Sparse token frequencies: descending cascade, each probe narrows the next.
				freqIdx64, _ := slices.BinarySearchFunc(freqs, docIdx64, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx63, _ := slices.BinarySearchFunc(freqs[:freqIdx64], docIdx63, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx62, _ := slices.BinarySearchFunc(freqs[:freqIdx63], docIdx62, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx61, _ := slices.BinarySearchFunc(freqs[:freqIdx62], docIdx61, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx60, _ := slices.BinarySearchFunc(freqs[:freqIdx61], docIdx60, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx59, _ := slices.BinarySearchFunc(freqs[:freqIdx60], docIdx59, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx58, _ := slices.BinarySearchFunc(freqs[:freqIdx59], docIdx58, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx57, _ := slices.BinarySearchFunc(freqs[:freqIdx58], docIdx57, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx56, _ := slices.BinarySearchFunc(freqs[:freqIdx57], docIdx56, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx55, _ := slices.BinarySearchFunc(freqs[:freqIdx56], docIdx55, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx54, _ := slices.BinarySearchFunc(freqs[:freqIdx55], docIdx54, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx53, _ := slices.BinarySearchFunc(freqs[:freqIdx54], docIdx53, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx52, _ := slices.BinarySearchFunc(freqs[:freqIdx53], docIdx52, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx51, _ := slices.BinarySearchFunc(freqs[:freqIdx52], docIdx51, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx50, _ := slices.BinarySearchFunc(freqs[:freqIdx51], docIdx50, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx49, _ := slices.BinarySearchFunc(freqs[:freqIdx50], docIdx49, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx48, _ := slices.BinarySearchFunc(freqs[:freqIdx49], docIdx48, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx47, _ := slices.BinarySearchFunc(freqs[:freqIdx48], docIdx47, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx46, _ := slices.BinarySearchFunc(freqs[:freqIdx47], docIdx46, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx45, _ := slices.BinarySearchFunc(freqs[:freqIdx46], docIdx45, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx44, _ := slices.BinarySearchFunc(freqs[:freqIdx45], docIdx44, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx43, _ := slices.BinarySearchFunc(freqs[:freqIdx44], docIdx43, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx42, _ := slices.BinarySearchFunc(freqs[:freqIdx43], docIdx42, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx41, _ := slices.BinarySearchFunc(freqs[:freqIdx42], docIdx41, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx40, _ := slices.BinarySearchFunc(freqs[:freqIdx41], docIdx40, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx39, _ := slices.BinarySearchFunc(freqs[:freqIdx40], docIdx39, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx38, _ := slices.BinarySearchFunc(freqs[:freqIdx39], docIdx38, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx37, _ := slices.BinarySearchFunc(freqs[:freqIdx38], docIdx37, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx36, _ := slices.BinarySearchFunc(freqs[:freqIdx37], docIdx36, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx35, _ := slices.BinarySearchFunc(freqs[:freqIdx36], docIdx35, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx34, _ := slices.BinarySearchFunc(freqs[:freqIdx35], docIdx34, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx33, _ := slices.BinarySearchFunc(freqs[:freqIdx34], docIdx33, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx32, _ := slices.BinarySearchFunc(freqs[:freqIdx33], docIdx32, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx31, _ := slices.BinarySearchFunc(freqs[:freqIdx32], docIdx31, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx30, _ := slices.BinarySearchFunc(freqs[:freqIdx31], docIdx30, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx29, _ := slices.BinarySearchFunc(freqs[:freqIdx30], docIdx29, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx28, _ := slices.BinarySearchFunc(freqs[:freqIdx29], docIdx28, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx27, _ := slices.BinarySearchFunc(freqs[:freqIdx28], docIdx27, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx26, _ := slices.BinarySearchFunc(freqs[:freqIdx27], docIdx26, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx25, _ := slices.BinarySearchFunc(freqs[:freqIdx26], docIdx25, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx24, _ := slices.BinarySearchFunc(freqs[:freqIdx25], docIdx24, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx23, _ := slices.BinarySearchFunc(freqs[:freqIdx24], docIdx23, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx22, _ := slices.BinarySearchFunc(freqs[:freqIdx23], docIdx22, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx21, _ := slices.BinarySearchFunc(freqs[:freqIdx22], docIdx21, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx20, _ := slices.BinarySearchFunc(freqs[:freqIdx21], docIdx20, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx19, _ := slices.BinarySearchFunc(freqs[:freqIdx20], docIdx19, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx18, _ := slices.BinarySearchFunc(freqs[:freqIdx19], docIdx18, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx17, _ := slices.BinarySearchFunc(freqs[:freqIdx18], docIdx17, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx16, _ := slices.BinarySearchFunc(freqs[:freqIdx17], docIdx16, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx15, _ := slices.BinarySearchFunc(freqs[:freqIdx16], docIdx15, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx14, _ := slices.BinarySearchFunc(freqs[:freqIdx15], docIdx14, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx13, _ := slices.BinarySearchFunc(freqs[:freqIdx14], docIdx13, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx12, _ := slices.BinarySearchFunc(freqs[:freqIdx13], docIdx12, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx11, _ := slices.BinarySearchFunc(freqs[:freqIdx12], docIdx11, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx10, _ := slices.BinarySearchFunc(freqs[:freqIdx11], docIdx10, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx09, _ := slices.BinarySearchFunc(freqs[:freqIdx10], docIdx09, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx08, _ := slices.BinarySearchFunc(freqs[:freqIdx09], docIdx08, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx07, _ := slices.BinarySearchFunc(freqs[:freqIdx08], docIdx07, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx06, _ := slices.BinarySearchFunc(freqs[:freqIdx07], docIdx06, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx05, _ := slices.BinarySearchFunc(freqs[:freqIdx06], docIdx05, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx04, _ := slices.BinarySearchFunc(freqs[:freqIdx05], docIdx04, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx03, _ := slices.BinarySearchFunc(freqs[:freqIdx04], docIdx03, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx02, _ := slices.BinarySearchFunc(freqs[:freqIdx03], docIdx02, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx01, _ := slices.BinarySearchFunc(freqs[:freqIdx02], docIdx01, CmpTokenFrequencyEntryAndDocumentIndex)

				tfsVec1[0], tfsVec1[1] = float32(freqs[freqIdx01].Frequency), float32(freqs[freqIdx02].Frequency)
				tfsVec1[2], tfsVec1[3] = float32(freqs[freqIdx03].Frequency), float32(freqs[freqIdx04].Frequency)
				tfsVec1[4], tfsVec1[5] = float32(freqs[freqIdx05].Frequency), float32(freqs[freqIdx06].Frequency)
				tfsVec1[6], tfsVec1[7] = float32(freqs[freqIdx07].Frequency), float32(freqs[freqIdx08].Frequency)
				tfsVec2[0], tfsVec2[1] = float32(freqs[freqIdx09].Frequency), float32(freqs[freqIdx10].Frequency)
				tfsVec2[2], tfsVec2[3] = float32(freqs[freqIdx11].Frequency), float32(freqs[freqIdx12].Frequency)
				tfsVec2[4], tfsVec2[5] = float32(freqs[freqIdx13].Frequency), float32(freqs[freqIdx14].Frequency)
				tfsVec2[6], tfsVec2[7] = float32(freqs[freqIdx15].Frequency), float32(freqs[freqIdx16].Frequency)
				tfsVec3[0], tfsVec3[1] = float32(freqs[freqIdx17].Frequency), float32(freqs[freqIdx18].Frequency)
				tfsVec3[2], tfsVec3[3] = float32(freqs[freqIdx19].Frequency), float32(freqs[freqIdx20].Frequency)
				tfsVec3[4], tfsVec3[5] = float32(freqs[freqIdx21].Frequency), float32(freqs[freqIdx22].Frequency)
				tfsVec3[6], tfsVec3[7] = float32(freqs[freqIdx23].Frequency), float32(freqs[freqIdx24].Frequency)
				tfsVec4[0], tfsVec4[1] = float32(freqs[freqIdx25].Frequency), float32(freqs[freqIdx26].Frequency)
				tfsVec4[2], tfsVec4[3] = float32(freqs[freqIdx27].Frequency), float32(freqs[freqIdx28].Frequency)
				tfsVec4[4], tfsVec4[5] = float32(freqs[freqIdx29].Frequency), float32(freqs[freqIdx30].Frequency)
				tfsVec4[6], tfsVec4[7] = float32(freqs[freqIdx31].Frequency), float32(freqs[freqIdx32].Frequency)
				tfsVec5[0], tfsVec5[1] = float32(freqs[freqIdx33].Frequency), float32(freqs[freqIdx34].Frequency)
				tfsVec5[2], tfsVec5[3] = float32(freqs[freqIdx35].Frequency), float32(freqs[freqIdx36].Frequency)
				tfsVec5[4], tfsVec5[5] = float32(freqs[freqIdx37].Frequency), float32(freqs[freqIdx38].Frequency)
				tfsVec5[6], tfsVec5[7] = float32(freqs[freqIdx39].Frequency), float32(freqs[freqIdx40].Frequency)
				tfsVec6[0], tfsVec6[1] = float32(freqs[freqIdx41].Frequency), float32(freqs[freqIdx42].Frequency)
				tfsVec6[2], tfsVec6[3] = float32(freqs[freqIdx43].Frequency), float32(freqs[freqIdx44].Frequency)
				tfsVec6[4], tfsVec6[5] = float32(freqs[freqIdx45].Frequency), float32(freqs[freqIdx46].Frequency)
				tfsVec6[6], tfsVec6[7] = float32(freqs[freqIdx47].Frequency), float32(freqs[freqIdx48].Frequency)
				tfsVec7[0], tfsVec7[1] = float32(freqs[freqIdx49].Frequency), float32(freqs[freqIdx50].Frequency)
				tfsVec7[2], tfsVec7[3] = float32(freqs[freqIdx51].Frequency), float32(freqs[freqIdx52].Frequency)
				tfsVec7[4], tfsVec7[5] = float32(freqs[freqIdx53].Frequency), float32(freqs[freqIdx54].Frequency)
				tfsVec7[6], tfsVec7[7] = float32(freqs[freqIdx55].Frequency), float32(freqs[freqIdx56].Frequency)
				tfsVec8[0], tfsVec8[1] = float32(freqs[freqIdx57].Frequency), float32(freqs[freqIdx58].Frequency)
				tfsVec8[2], tfsVec8[3] = float32(freqs[freqIdx59].Frequency), float32(freqs[freqIdx60].Frequency)
				tfsVec8[4], tfsVec8[5] = float32(freqs[freqIdx61].Frequency), float32(freqs[freqIdx62].Frequency)
				tfsVec8[6], tfsVec8[7] = float32(freqs[freqIdx63].Frequency), float32(freqs[freqIdx64].Frequency)
				freqs = freqs[1+freqIdx64:]

				dls1 := archsimd.LoadFloat32x8Array(&dlsVec1)
				tfs1 := archsimd.LoadFloat32x8Array(&tfsVec1)
				partial1 := tfs1.Add(satXoneMinuxLps)
				denominator1 := dls1.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial1)
				reciprocal1 := denominator1.Reciprocal()
				tfNorms1 := tfs1.Mul(reciprocal1)
				scores1 := idfBoosts.Mul(tfNorms1)
				scores1.StoreArray(&scoresOut1)

				dls2 := archsimd.LoadFloat32x8Array(&dlsVec2)
				tfs2 := archsimd.LoadFloat32x8Array(&tfsVec2)
				partial2 := tfs2.Add(satXoneMinuxLps)
				denominator2 := dls2.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial2)
				reciprocal2 := denominator2.Reciprocal()
				tfNorms2 := tfs2.Mul(reciprocal2)
				scores2 := idfBoosts.Mul(tfNorms2)
				scores2.StoreArray(&scoresOut2)

				dls3 := archsimd.LoadFloat32x8Array(&dlsVec3)
				tfs3 := archsimd.LoadFloat32x8Array(&tfsVec3)
				partial3 := tfs3.Add(satXoneMinuxLps)
				denominator3 := dls3.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial3)
				reciprocal3 := denominator3.Reciprocal()
				tfNorms3 := tfs3.Mul(reciprocal3)
				scores3 := idfBoosts.Mul(tfNorms3)
				scores3.StoreArray(&scoresOut3)

				dls4 := archsimd.LoadFloat32x8Array(&dlsVec4)
				tfs4 := archsimd.LoadFloat32x8Array(&tfsVec4)
				partial4 := tfs4.Add(satXoneMinuxLps)
				denominator4 := dls4.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial4)
				reciprocal4 := denominator4.Reciprocal()
				tfNorms4 := tfs4.Mul(reciprocal4)
				scores4 := idfBoosts.Mul(tfNorms4)
				scores4.StoreArray(&scoresOut4)

				dls5 := archsimd.LoadFloat32x8Array(&dlsVec5)
				tfs5 := archsimd.LoadFloat32x8Array(&tfsVec5)
				partial5 := tfs5.Add(satXoneMinuxLps)
				denominator5 := dls5.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial5)
				reciprocal5 := denominator5.Reciprocal()
				tfNorms5 := tfs5.Mul(reciprocal5)
				scores5 := idfBoosts.Mul(tfNorms5)
				scores5.StoreArray(&scoresOut5)

				dls6 := archsimd.LoadFloat32x8Array(&dlsVec6)
				tfs6 := archsimd.LoadFloat32x8Array(&tfsVec6)
				partial6 := tfs6.Add(satXoneMinuxLps)
				denominator6 := dls6.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial6)
				reciprocal6 := denominator6.Reciprocal()
				tfNorms6 := tfs6.Mul(reciprocal6)
				scores6 := idfBoosts.Mul(tfNorms6)
				scores6.StoreArray(&scoresOut6)

				dls7 := archsimd.LoadFloat32x8Array(&dlsVec7)
				tfs7 := archsimd.LoadFloat32x8Array(&tfsVec7)
				partial7 := tfs7.Add(satXoneMinuxLps)
				denominator7 := dls7.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial7)
				reciprocal7 := denominator7.Reciprocal()
				tfNorms7 := tfs7.Mul(reciprocal7)
				scores7 := idfBoosts.Mul(tfNorms7)
				scores7.StoreArray(&scoresOut7)

				dls8 := archsimd.LoadFloat32x8Array(&dlsVec8)
				tfs8 := archsimd.LoadFloat32x8Array(&tfsVec8)
				partial8 := tfs8.Add(satXoneMinuxLps)
				denominator8 := dls8.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial8)
				reciprocal8 := denominator8.Reciprocal()
				tfNorms8 := tfs8.Mul(reciprocal8)
				scores8 := idfBoosts.Mul(tfNorms8)
				scores8.StoreArray(&scoresOut8)

				guess = ctx.Scoring.Add(guess, docIdx01, scoresOut1[0])
				guess = ctx.Scoring.Add(guess, docIdx02, scoresOut1[1])
				guess = ctx.Scoring.Add(guess, docIdx03, scoresOut1[2])
				guess = ctx.Scoring.Add(guess, docIdx04, scoresOut1[3])
				guess = ctx.Scoring.Add(guess, docIdx05, scoresOut1[4])
				guess = ctx.Scoring.Add(guess, docIdx06, scoresOut1[5])
				guess = ctx.Scoring.Add(guess, docIdx07, scoresOut1[6])
				guess = ctx.Scoring.Add(guess, docIdx08, scoresOut1[7])

				guess = ctx.Scoring.Add(guess, docIdx09, scoresOut2[0])
				guess = ctx.Scoring.Add(guess, docIdx10, scoresOut2[1])
				guess = ctx.Scoring.Add(guess, docIdx11, scoresOut2[2])
				guess = ctx.Scoring.Add(guess, docIdx12, scoresOut2[3])
				guess = ctx.Scoring.Add(guess, docIdx13, scoresOut2[4])
				guess = ctx.Scoring.Add(guess, docIdx14, scoresOut2[5])
				guess = ctx.Scoring.Add(guess, docIdx15, scoresOut2[6])
				guess = ctx.Scoring.Add(guess, docIdx16, scoresOut2[7])

				guess = ctx.Scoring.Add(guess, docIdx17, scoresOut3[0])
				guess = ctx.Scoring.Add(guess, docIdx18, scoresOut3[1])
				guess = ctx.Scoring.Add(guess, docIdx19, scoresOut3[2])
				guess = ctx.Scoring.Add(guess, docIdx20, scoresOut3[3])
				guess = ctx.Scoring.Add(guess, docIdx21, scoresOut3[4])
				guess = ctx.Scoring.Add(guess, docIdx22, scoresOut3[5])
				guess = ctx.Scoring.Add(guess, docIdx23, scoresOut3[6])
				guess = ctx.Scoring.Add(guess, docIdx24, scoresOut3[7])

				guess = ctx.Scoring.Add(guess, docIdx25, scoresOut4[0])
				guess = ctx.Scoring.Add(guess, docIdx26, scoresOut4[1])
				guess = ctx.Scoring.Add(guess, docIdx27, scoresOut4[2])
				guess = ctx.Scoring.Add(guess, docIdx28, scoresOut4[3])
				guess = ctx.Scoring.Add(guess, docIdx29, scoresOut4[4])
				guess = ctx.Scoring.Add(guess, docIdx30, scoresOut4[5])
				guess = ctx.Scoring.Add(guess, docIdx31, scoresOut4[6])
				guess = ctx.Scoring.Add(guess, docIdx32, scoresOut4[7])

				guess = ctx.Scoring.Add(guess, docIdx33, scoresOut5[0])
				guess = ctx.Scoring.Add(guess, docIdx34, scoresOut5[1])
				guess = ctx.Scoring.Add(guess, docIdx35, scoresOut5[2])
				guess = ctx.Scoring.Add(guess, docIdx36, scoresOut5[3])
				guess = ctx.Scoring.Add(guess, docIdx37, scoresOut5[4])
				guess = ctx.Scoring.Add(guess, docIdx38, scoresOut5[5])
				guess = ctx.Scoring.Add(guess, docIdx39, scoresOut5[6])
				guess = ctx.Scoring.Add(guess, docIdx40, scoresOut5[7])

				guess = ctx.Scoring.Add(guess, docIdx41, scoresOut6[0])
				guess = ctx.Scoring.Add(guess, docIdx42, scoresOut6[1])
				guess = ctx.Scoring.Add(guess, docIdx43, scoresOut6[2])
				guess = ctx.Scoring.Add(guess, docIdx44, scoresOut6[3])
				guess = ctx.Scoring.Add(guess, docIdx45, scoresOut6[4])
				guess = ctx.Scoring.Add(guess, docIdx46, scoresOut6[5])
				guess = ctx.Scoring.Add(guess, docIdx47, scoresOut6[6])
				guess = ctx.Scoring.Add(guess, docIdx48, scoresOut6[7])

				guess = ctx.Scoring.Add(guess, docIdx49, scoresOut7[0])
				guess = ctx.Scoring.Add(guess, docIdx50, scoresOut7[1])
				guess = ctx.Scoring.Add(guess, docIdx51, scoresOut7[2])
				guess = ctx.Scoring.Add(guess, docIdx52, scoresOut7[3])
				guess = ctx.Scoring.Add(guess, docIdx53, scoresOut7[4])
				guess = ctx.Scoring.Add(guess, docIdx54, scoresOut7[5])
				guess = ctx.Scoring.Add(guess, docIdx55, scoresOut7[6])
				guess = ctx.Scoring.Add(guess, docIdx56, scoresOut7[7])

				guess = ctx.Scoring.Add(guess, docIdx57, scoresOut8[0])
				guess = ctx.Scoring.Add(guess, docIdx58, scoresOut8[1])
				guess = ctx.Scoring.Add(guess, docIdx59, scoresOut8[2])
				guess = ctx.Scoring.Add(guess, docIdx60, scoresOut8[3])
				guess = ctx.Scoring.Add(guess, docIdx61, scoresOut8[4])
				guess = ctx.Scoring.Add(guess, docIdx62, scoresOut8[5])
				guess = ctx.Scoring.Add(guess, docIdx63, scoresOut8[6])
				guess = ctx.Scoring.Add(guess, docIdx64, scoresOut8[7])
			}

			// Remainder that still fills one whole vector.
			for i := n64; i < n8; i += LaneCountAvx2 {
				docIdx01, docIdx02, docIdx03, docIdx04 := resolved[0+i], resolved[1+i], resolved[2+i], resolved[3+i]
				docIdx05, docIdx06, docIdx07, docIdx08 := resolved[4+i], resolved[5+i], resolved[6+i], resolved[7+i]

				// Dense document lengths: index directly, no probe needed.
				dlsVec1[0], dlsVec1[1] = float32(docLengths[docIdx01].Length), float32(docLengths[docIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docIdx03].Length), float32(docLengths[docIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docIdx05].Length), float32(docLengths[docIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docIdx07].Length), float32(docLengths[docIdx08].Length)

				// Sparse token frequencies: descending cascade, each probe narrows the next.
				freqIdx08, _ := slices.BinarySearchFunc(freqs, docIdx08, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx07, _ := slices.BinarySearchFunc(freqs[:freqIdx08], docIdx07, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx06, _ := slices.BinarySearchFunc(freqs[:freqIdx07], docIdx06, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx05, _ := slices.BinarySearchFunc(freqs[:freqIdx06], docIdx05, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx04, _ := slices.BinarySearchFunc(freqs[:freqIdx05], docIdx04, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx03, _ := slices.BinarySearchFunc(freqs[:freqIdx04], docIdx03, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx02, _ := slices.BinarySearchFunc(freqs[:freqIdx03], docIdx02, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx01, _ := slices.BinarySearchFunc(freqs[:freqIdx02], docIdx01, CmpTokenFrequencyEntryAndDocumentIndex)

				tfsVec1[0], tfsVec1[1] = float32(freqs[freqIdx01].Frequency), float32(freqs[freqIdx02].Frequency)
				tfsVec1[2], tfsVec1[3] = float32(freqs[freqIdx03].Frequency), float32(freqs[freqIdx04].Frequency)
				tfsVec1[4], tfsVec1[5] = float32(freqs[freqIdx05].Frequency), float32(freqs[freqIdx06].Frequency)
				tfsVec1[6], tfsVec1[7] = float32(freqs[freqIdx07].Frequency), float32(freqs[freqIdx08].Frequency)
				freqs = freqs[1+freqIdx08:]

				dls1 := archsimd.LoadFloat32x8Array(&dlsVec1)
				tfs1 := archsimd.LoadFloat32x8Array(&tfsVec1)
				partial1 := tfs1.Add(satXoneMinuxLps)
				denominator1 := dls1.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial1)
				reciprocal1 := denominator1.Reciprocal()
				tfNorms1 := tfs1.Mul(reciprocal1)
				scores1 := idfBoosts.Mul(tfNorms1)
				scores1.StoreArray(&scoresOut1)

				guess = ctx.Scoring.Add(guess, docIdx01, scoresOut1[0])
				guess = ctx.Scoring.Add(guess, docIdx02, scoresOut1[1])
				guess = ctx.Scoring.Add(guess, docIdx03, scoresOut1[2])
				guess = ctx.Scoring.Add(guess, docIdx04, scoresOut1[3])
				guess = ctx.Scoring.Add(guess, docIdx05, scoresOut1[4])
				guess = ctx.Scoring.Add(guess, docIdx06, scoresOut1[5])
				guess = ctx.Scoring.Add(guess, docIdx07, scoresOut1[6])
				guess = ctx.Scoring.Add(guess, docIdx08, scoresOut1[7])
			}

			// Final < 8 documents, scalar.
			for i := n8; i < len(resolved); i++ {
				docIdx := resolved[i]

				dl := float32(docLengths[docIdx].Length)
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
			// 64 documents per iteration: 8 x Float32x8.
			for i := 0; i < n64; i += UnrollingFactorAvx2 {
				docIdx01, docIdx02, docIdx03, docIdx04 := resolved[0+i], resolved[1+i], resolved[2+i], resolved[3+i]
				docIdx05, docIdx06, docIdx07, docIdx08 := resolved[4+i], resolved[5+i], resolved[6+i], resolved[7+i]
				docIdx09, docIdx10, docIdx11, docIdx12 := resolved[8+i], resolved[9+i], resolved[10+i], resolved[11+i]
				docIdx13, docIdx14, docIdx15, docIdx16 := resolved[12+i], resolved[13+i], resolved[14+i], resolved[15+i]
				docIdx17, docIdx18, docIdx19, docIdx20 := resolved[16+i], resolved[17+i], resolved[18+i], resolved[19+i]
				docIdx21, docIdx22, docIdx23, docIdx24 := resolved[20+i], resolved[21+i], resolved[22+i], resolved[23+i]
				docIdx25, docIdx26, docIdx27, docIdx28 := resolved[24+i], resolved[25+i], resolved[26+i], resolved[27+i]
				docIdx29, docIdx30, docIdx31, docIdx32 := resolved[28+i], resolved[29+i], resolved[30+i], resolved[31+i]
				docIdx33, docIdx34, docIdx35, docIdx36 := resolved[32+i], resolved[33+i], resolved[34+i], resolved[35+i]
				docIdx37, docIdx38, docIdx39, docIdx40 := resolved[36+i], resolved[37+i], resolved[38+i], resolved[39+i]
				docIdx41, docIdx42, docIdx43, docIdx44 := resolved[40+i], resolved[41+i], resolved[42+i], resolved[43+i]
				docIdx45, docIdx46, docIdx47, docIdx48 := resolved[44+i], resolved[45+i], resolved[46+i], resolved[47+i]
				docIdx49, docIdx50, docIdx51, docIdx52 := resolved[48+i], resolved[49+i], resolved[50+i], resolved[51+i]
				docIdx53, docIdx54, docIdx55, docIdx56 := resolved[52+i], resolved[53+i], resolved[54+i], resolved[55+i]
				docIdx57, docIdx58, docIdx59, docIdx60 := resolved[56+i], resolved[57+i], resolved[58+i], resolved[59+i]
				docIdx61, docIdx62, docIdx63, docIdx64 := resolved[60+i], resolved[61+i], resolved[62+i], resolved[63+i]

				// Sparse document lengths: descending cascade, each probe narrows the next.
				docLengthIdx64, _ := slices.BinarySearchFunc(docLengths, docIdx64, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx63, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx64], docIdx63, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx62, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx63], docIdx62, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx61, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx62], docIdx61, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx60, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx61], docIdx60, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx59, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx60], docIdx59, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx58, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx59], docIdx58, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx57, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx58], docIdx57, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx56, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx57], docIdx56, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx55, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx56], docIdx55, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx54, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx55], docIdx54, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx53, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx54], docIdx53, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx52, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx53], docIdx52, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx51, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx52], docIdx51, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx50, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx51], docIdx50, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx49, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx50], docIdx49, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx48, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx49], docIdx48, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx47, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx48], docIdx47, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx46, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx47], docIdx46, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx45, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx46], docIdx45, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx44, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx45], docIdx44, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx43, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx44], docIdx43, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx42, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx43], docIdx42, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx41, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx42], docIdx41, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx40, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx41], docIdx40, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx39, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx40], docIdx39, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx38, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx39], docIdx38, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx37, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx38], docIdx37, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx36, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx37], docIdx36, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx35, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx36], docIdx35, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx34, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx35], docIdx34, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx33, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx34], docIdx33, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx32, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx33], docIdx32, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx31, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx32], docIdx31, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx30, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx31], docIdx30, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx29, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx30], docIdx29, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx28, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx29], docIdx28, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx27, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx28], docIdx27, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx26, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx27], docIdx26, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx25, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx26], docIdx25, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx24, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx25], docIdx24, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx23, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx24], docIdx23, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx22, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx23], docIdx22, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx21, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx22], docIdx21, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx20, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx21], docIdx20, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx19, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx20], docIdx19, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx18, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx19], docIdx18, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx17, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx18], docIdx17, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx16, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx17], docIdx16, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx15, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx16], docIdx15, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx14, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx15], docIdx14, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx13, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx14], docIdx13, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx12, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx13], docIdx12, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx11, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx12], docIdx11, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx10, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx11], docIdx10, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx09, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx10], docIdx09, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx08, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx09], docIdx08, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx07, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx08], docIdx07, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx06, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx07], docIdx06, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx05, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx06], docIdx05, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx04, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx05], docIdx04, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx03, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx04], docIdx03, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx02, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx03], docIdx02, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx01, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx02], docIdx01, CmpDocumentLengthEntryAndDocumentIndex)

				dlsVec1[0], dlsVec1[1] = float32(docLengths[docLengthIdx01].Length), float32(docLengths[docLengthIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docLengthIdx03].Length), float32(docLengths[docLengthIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docLengthIdx05].Length), float32(docLengths[docLengthIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docLengthIdx07].Length), float32(docLengths[docLengthIdx08].Length)
				dlsVec2[0], dlsVec2[1] = float32(docLengths[docLengthIdx09].Length), float32(docLengths[docLengthIdx10].Length)
				dlsVec2[2], dlsVec2[3] = float32(docLengths[docLengthIdx11].Length), float32(docLengths[docLengthIdx12].Length)
				dlsVec2[4], dlsVec2[5] = float32(docLengths[docLengthIdx13].Length), float32(docLengths[docLengthIdx14].Length)
				dlsVec2[6], dlsVec2[7] = float32(docLengths[docLengthIdx15].Length), float32(docLengths[docLengthIdx16].Length)
				dlsVec3[0], dlsVec3[1] = float32(docLengths[docLengthIdx17].Length), float32(docLengths[docLengthIdx18].Length)
				dlsVec3[2], dlsVec3[3] = float32(docLengths[docLengthIdx19].Length), float32(docLengths[docLengthIdx20].Length)
				dlsVec3[4], dlsVec3[5] = float32(docLengths[docLengthIdx21].Length), float32(docLengths[docLengthIdx22].Length)
				dlsVec3[6], dlsVec3[7] = float32(docLengths[docLengthIdx23].Length), float32(docLengths[docLengthIdx24].Length)
				dlsVec4[0], dlsVec4[1] = float32(docLengths[docLengthIdx25].Length), float32(docLengths[docLengthIdx26].Length)
				dlsVec4[2], dlsVec4[3] = float32(docLengths[docLengthIdx27].Length), float32(docLengths[docLengthIdx28].Length)
				dlsVec4[4], dlsVec4[5] = float32(docLengths[docLengthIdx29].Length), float32(docLengths[docLengthIdx30].Length)
				dlsVec4[6], dlsVec4[7] = float32(docLengths[docLengthIdx31].Length), float32(docLengths[docLengthIdx32].Length)
				dlsVec5[0], dlsVec5[1] = float32(docLengths[docLengthIdx33].Length), float32(docLengths[docLengthIdx34].Length)
				dlsVec5[2], dlsVec5[3] = float32(docLengths[docLengthIdx35].Length), float32(docLengths[docLengthIdx36].Length)
				dlsVec5[4], dlsVec5[5] = float32(docLengths[docLengthIdx37].Length), float32(docLengths[docLengthIdx38].Length)
				dlsVec5[6], dlsVec5[7] = float32(docLengths[docLengthIdx39].Length), float32(docLengths[docLengthIdx40].Length)
				dlsVec6[0], dlsVec6[1] = float32(docLengths[docLengthIdx41].Length), float32(docLengths[docLengthIdx42].Length)
				dlsVec6[2], dlsVec6[3] = float32(docLengths[docLengthIdx43].Length), float32(docLengths[docLengthIdx44].Length)
				dlsVec6[4], dlsVec6[5] = float32(docLengths[docLengthIdx45].Length), float32(docLengths[docLengthIdx46].Length)
				dlsVec6[6], dlsVec6[7] = float32(docLengths[docLengthIdx47].Length), float32(docLengths[docLengthIdx48].Length)
				dlsVec7[0], dlsVec7[1] = float32(docLengths[docLengthIdx49].Length), float32(docLengths[docLengthIdx50].Length)
				dlsVec7[2], dlsVec7[3] = float32(docLengths[docLengthIdx51].Length), float32(docLengths[docLengthIdx52].Length)
				dlsVec7[4], dlsVec7[5] = float32(docLengths[docLengthIdx53].Length), float32(docLengths[docLengthIdx54].Length)
				dlsVec7[6], dlsVec7[7] = float32(docLengths[docLengthIdx55].Length), float32(docLengths[docLengthIdx56].Length)
				dlsVec8[0], dlsVec8[1] = float32(docLengths[docLengthIdx57].Length), float32(docLengths[docLengthIdx58].Length)
				dlsVec8[2], dlsVec8[3] = float32(docLengths[docLengthIdx59].Length), float32(docLengths[docLengthIdx60].Length)
				dlsVec8[4], dlsVec8[5] = float32(docLengths[docLengthIdx61].Length), float32(docLengths[docLengthIdx62].Length)
				dlsVec8[6], dlsVec8[7] = float32(docLengths[docLengthIdx63].Length), float32(docLengths[docLengthIdx64].Length)
				docLengths = docLengths[1+docLengthIdx64:]

				// Sparse token frequencies: descending cascade, each probe narrows the next.
				freqIdx64, _ := slices.BinarySearchFunc(freqs, docIdx64, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx63, _ := slices.BinarySearchFunc(freqs[:freqIdx64], docIdx63, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx62, _ := slices.BinarySearchFunc(freqs[:freqIdx63], docIdx62, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx61, _ := slices.BinarySearchFunc(freqs[:freqIdx62], docIdx61, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx60, _ := slices.BinarySearchFunc(freqs[:freqIdx61], docIdx60, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx59, _ := slices.BinarySearchFunc(freqs[:freqIdx60], docIdx59, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx58, _ := slices.BinarySearchFunc(freqs[:freqIdx59], docIdx58, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx57, _ := slices.BinarySearchFunc(freqs[:freqIdx58], docIdx57, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx56, _ := slices.BinarySearchFunc(freqs[:freqIdx57], docIdx56, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx55, _ := slices.BinarySearchFunc(freqs[:freqIdx56], docIdx55, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx54, _ := slices.BinarySearchFunc(freqs[:freqIdx55], docIdx54, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx53, _ := slices.BinarySearchFunc(freqs[:freqIdx54], docIdx53, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx52, _ := slices.BinarySearchFunc(freqs[:freqIdx53], docIdx52, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx51, _ := slices.BinarySearchFunc(freqs[:freqIdx52], docIdx51, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx50, _ := slices.BinarySearchFunc(freqs[:freqIdx51], docIdx50, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx49, _ := slices.BinarySearchFunc(freqs[:freqIdx50], docIdx49, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx48, _ := slices.BinarySearchFunc(freqs[:freqIdx49], docIdx48, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx47, _ := slices.BinarySearchFunc(freqs[:freqIdx48], docIdx47, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx46, _ := slices.BinarySearchFunc(freqs[:freqIdx47], docIdx46, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx45, _ := slices.BinarySearchFunc(freqs[:freqIdx46], docIdx45, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx44, _ := slices.BinarySearchFunc(freqs[:freqIdx45], docIdx44, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx43, _ := slices.BinarySearchFunc(freqs[:freqIdx44], docIdx43, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx42, _ := slices.BinarySearchFunc(freqs[:freqIdx43], docIdx42, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx41, _ := slices.BinarySearchFunc(freqs[:freqIdx42], docIdx41, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx40, _ := slices.BinarySearchFunc(freqs[:freqIdx41], docIdx40, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx39, _ := slices.BinarySearchFunc(freqs[:freqIdx40], docIdx39, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx38, _ := slices.BinarySearchFunc(freqs[:freqIdx39], docIdx38, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx37, _ := slices.BinarySearchFunc(freqs[:freqIdx38], docIdx37, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx36, _ := slices.BinarySearchFunc(freqs[:freqIdx37], docIdx36, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx35, _ := slices.BinarySearchFunc(freqs[:freqIdx36], docIdx35, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx34, _ := slices.BinarySearchFunc(freqs[:freqIdx35], docIdx34, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx33, _ := slices.BinarySearchFunc(freqs[:freqIdx34], docIdx33, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx32, _ := slices.BinarySearchFunc(freqs[:freqIdx33], docIdx32, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx31, _ := slices.BinarySearchFunc(freqs[:freqIdx32], docIdx31, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx30, _ := slices.BinarySearchFunc(freqs[:freqIdx31], docIdx30, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx29, _ := slices.BinarySearchFunc(freqs[:freqIdx30], docIdx29, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx28, _ := slices.BinarySearchFunc(freqs[:freqIdx29], docIdx28, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx27, _ := slices.BinarySearchFunc(freqs[:freqIdx28], docIdx27, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx26, _ := slices.BinarySearchFunc(freqs[:freqIdx27], docIdx26, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx25, _ := slices.BinarySearchFunc(freqs[:freqIdx26], docIdx25, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx24, _ := slices.BinarySearchFunc(freqs[:freqIdx25], docIdx24, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx23, _ := slices.BinarySearchFunc(freqs[:freqIdx24], docIdx23, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx22, _ := slices.BinarySearchFunc(freqs[:freqIdx23], docIdx22, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx21, _ := slices.BinarySearchFunc(freqs[:freqIdx22], docIdx21, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx20, _ := slices.BinarySearchFunc(freqs[:freqIdx21], docIdx20, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx19, _ := slices.BinarySearchFunc(freqs[:freqIdx20], docIdx19, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx18, _ := slices.BinarySearchFunc(freqs[:freqIdx19], docIdx18, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx17, _ := slices.BinarySearchFunc(freqs[:freqIdx18], docIdx17, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx16, _ := slices.BinarySearchFunc(freqs[:freqIdx17], docIdx16, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx15, _ := slices.BinarySearchFunc(freqs[:freqIdx16], docIdx15, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx14, _ := slices.BinarySearchFunc(freqs[:freqIdx15], docIdx14, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx13, _ := slices.BinarySearchFunc(freqs[:freqIdx14], docIdx13, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx12, _ := slices.BinarySearchFunc(freqs[:freqIdx13], docIdx12, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx11, _ := slices.BinarySearchFunc(freqs[:freqIdx12], docIdx11, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx10, _ := slices.BinarySearchFunc(freqs[:freqIdx11], docIdx10, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx09, _ := slices.BinarySearchFunc(freqs[:freqIdx10], docIdx09, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx08, _ := slices.BinarySearchFunc(freqs[:freqIdx09], docIdx08, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx07, _ := slices.BinarySearchFunc(freqs[:freqIdx08], docIdx07, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx06, _ := slices.BinarySearchFunc(freqs[:freqIdx07], docIdx06, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx05, _ := slices.BinarySearchFunc(freqs[:freqIdx06], docIdx05, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx04, _ := slices.BinarySearchFunc(freqs[:freqIdx05], docIdx04, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx03, _ := slices.BinarySearchFunc(freqs[:freqIdx04], docIdx03, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx02, _ := slices.BinarySearchFunc(freqs[:freqIdx03], docIdx02, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx01, _ := slices.BinarySearchFunc(freqs[:freqIdx02], docIdx01, CmpTokenFrequencyEntryAndDocumentIndex)

				tfsVec1[0], tfsVec1[1] = float32(freqs[freqIdx01].Frequency), float32(freqs[freqIdx02].Frequency)
				tfsVec1[2], tfsVec1[3] = float32(freqs[freqIdx03].Frequency), float32(freqs[freqIdx04].Frequency)
				tfsVec1[4], tfsVec1[5] = float32(freqs[freqIdx05].Frequency), float32(freqs[freqIdx06].Frequency)
				tfsVec1[6], tfsVec1[7] = float32(freqs[freqIdx07].Frequency), float32(freqs[freqIdx08].Frequency)
				tfsVec2[0], tfsVec2[1] = float32(freqs[freqIdx09].Frequency), float32(freqs[freqIdx10].Frequency)
				tfsVec2[2], tfsVec2[3] = float32(freqs[freqIdx11].Frequency), float32(freqs[freqIdx12].Frequency)
				tfsVec2[4], tfsVec2[5] = float32(freqs[freqIdx13].Frequency), float32(freqs[freqIdx14].Frequency)
				tfsVec2[6], tfsVec2[7] = float32(freqs[freqIdx15].Frequency), float32(freqs[freqIdx16].Frequency)
				tfsVec3[0], tfsVec3[1] = float32(freqs[freqIdx17].Frequency), float32(freqs[freqIdx18].Frequency)
				tfsVec3[2], tfsVec3[3] = float32(freqs[freqIdx19].Frequency), float32(freqs[freqIdx20].Frequency)
				tfsVec3[4], tfsVec3[5] = float32(freqs[freqIdx21].Frequency), float32(freqs[freqIdx22].Frequency)
				tfsVec3[6], tfsVec3[7] = float32(freqs[freqIdx23].Frequency), float32(freqs[freqIdx24].Frequency)
				tfsVec4[0], tfsVec4[1] = float32(freqs[freqIdx25].Frequency), float32(freqs[freqIdx26].Frequency)
				tfsVec4[2], tfsVec4[3] = float32(freqs[freqIdx27].Frequency), float32(freqs[freqIdx28].Frequency)
				tfsVec4[4], tfsVec4[5] = float32(freqs[freqIdx29].Frequency), float32(freqs[freqIdx30].Frequency)
				tfsVec4[6], tfsVec4[7] = float32(freqs[freqIdx31].Frequency), float32(freqs[freqIdx32].Frequency)
				tfsVec5[0], tfsVec5[1] = float32(freqs[freqIdx33].Frequency), float32(freqs[freqIdx34].Frequency)
				tfsVec5[2], tfsVec5[3] = float32(freqs[freqIdx35].Frequency), float32(freqs[freqIdx36].Frequency)
				tfsVec5[4], tfsVec5[5] = float32(freqs[freqIdx37].Frequency), float32(freqs[freqIdx38].Frequency)
				tfsVec5[6], tfsVec5[7] = float32(freqs[freqIdx39].Frequency), float32(freqs[freqIdx40].Frequency)
				tfsVec6[0], tfsVec6[1] = float32(freqs[freqIdx41].Frequency), float32(freqs[freqIdx42].Frequency)
				tfsVec6[2], tfsVec6[3] = float32(freqs[freqIdx43].Frequency), float32(freqs[freqIdx44].Frequency)
				tfsVec6[4], tfsVec6[5] = float32(freqs[freqIdx45].Frequency), float32(freqs[freqIdx46].Frequency)
				tfsVec6[6], tfsVec6[7] = float32(freqs[freqIdx47].Frequency), float32(freqs[freqIdx48].Frequency)
				tfsVec7[0], tfsVec7[1] = float32(freqs[freqIdx49].Frequency), float32(freqs[freqIdx50].Frequency)
				tfsVec7[2], tfsVec7[3] = float32(freqs[freqIdx51].Frequency), float32(freqs[freqIdx52].Frequency)
				tfsVec7[4], tfsVec7[5] = float32(freqs[freqIdx53].Frequency), float32(freqs[freqIdx54].Frequency)
				tfsVec7[6], tfsVec7[7] = float32(freqs[freqIdx55].Frequency), float32(freqs[freqIdx56].Frequency)
				tfsVec8[0], tfsVec8[1] = float32(freqs[freqIdx57].Frequency), float32(freqs[freqIdx58].Frequency)
				tfsVec8[2], tfsVec8[3] = float32(freqs[freqIdx59].Frequency), float32(freqs[freqIdx60].Frequency)
				tfsVec8[4], tfsVec8[5] = float32(freqs[freqIdx61].Frequency), float32(freqs[freqIdx62].Frequency)
				tfsVec8[6], tfsVec8[7] = float32(freqs[freqIdx63].Frequency), float32(freqs[freqIdx64].Frequency)
				freqs = freqs[1+freqIdx64:]

				dls1 := archsimd.LoadFloat32x8Array(&dlsVec1)
				tfs1 := archsimd.LoadFloat32x8Array(&tfsVec1)
				partial1 := tfs1.Add(satXoneMinuxLps)
				denominator1 := dls1.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial1)
				reciprocal1 := denominator1.Reciprocal()
				tfNorms1 := tfs1.Mul(reciprocal1)
				scores1 := idfBoosts.Mul(tfNorms1)
				scores1.StoreArray(&scoresOut1)

				dls2 := archsimd.LoadFloat32x8Array(&dlsVec2)
				tfs2 := archsimd.LoadFloat32x8Array(&tfsVec2)
				partial2 := tfs2.Add(satXoneMinuxLps)
				denominator2 := dls2.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial2)
				reciprocal2 := denominator2.Reciprocal()
				tfNorms2 := tfs2.Mul(reciprocal2)
				scores2 := idfBoosts.Mul(tfNorms2)
				scores2.StoreArray(&scoresOut2)

				dls3 := archsimd.LoadFloat32x8Array(&dlsVec3)
				tfs3 := archsimd.LoadFloat32x8Array(&tfsVec3)
				partial3 := tfs3.Add(satXoneMinuxLps)
				denominator3 := dls3.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial3)
				reciprocal3 := denominator3.Reciprocal()
				tfNorms3 := tfs3.Mul(reciprocal3)
				scores3 := idfBoosts.Mul(tfNorms3)
				scores3.StoreArray(&scoresOut3)

				dls4 := archsimd.LoadFloat32x8Array(&dlsVec4)
				tfs4 := archsimd.LoadFloat32x8Array(&tfsVec4)
				partial4 := tfs4.Add(satXoneMinuxLps)
				denominator4 := dls4.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial4)
				reciprocal4 := denominator4.Reciprocal()
				tfNorms4 := tfs4.Mul(reciprocal4)
				scores4 := idfBoosts.Mul(tfNorms4)
				scores4.StoreArray(&scoresOut4)

				dls5 := archsimd.LoadFloat32x8Array(&dlsVec5)
				tfs5 := archsimd.LoadFloat32x8Array(&tfsVec5)
				partial5 := tfs5.Add(satXoneMinuxLps)
				denominator5 := dls5.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial5)
				reciprocal5 := denominator5.Reciprocal()
				tfNorms5 := tfs5.Mul(reciprocal5)
				scores5 := idfBoosts.Mul(tfNorms5)
				scores5.StoreArray(&scoresOut5)

				dls6 := archsimd.LoadFloat32x8Array(&dlsVec6)
				tfs6 := archsimd.LoadFloat32x8Array(&tfsVec6)
				partial6 := tfs6.Add(satXoneMinuxLps)
				denominator6 := dls6.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial6)
				reciprocal6 := denominator6.Reciprocal()
				tfNorms6 := tfs6.Mul(reciprocal6)
				scores6 := idfBoosts.Mul(tfNorms6)
				scores6.StoreArray(&scoresOut6)

				dls7 := archsimd.LoadFloat32x8Array(&dlsVec7)
				tfs7 := archsimd.LoadFloat32x8Array(&tfsVec7)
				partial7 := tfs7.Add(satXoneMinuxLps)
				denominator7 := dls7.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial7)
				reciprocal7 := denominator7.Reciprocal()
				tfNorms7 := tfs7.Mul(reciprocal7)
				scores7 := idfBoosts.Mul(tfNorms7)
				scores7.StoreArray(&scoresOut7)

				dls8 := archsimd.LoadFloat32x8Array(&dlsVec8)
				tfs8 := archsimd.LoadFloat32x8Array(&tfsVec8)
				partial8 := tfs8.Add(satXoneMinuxLps)
				denominator8 := dls8.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial8)
				reciprocal8 := denominator8.Reciprocal()
				tfNorms8 := tfs8.Mul(reciprocal8)
				scores8 := idfBoosts.Mul(tfNorms8)
				scores8.StoreArray(&scoresOut8)

				guess = ctx.Scoring.Add(guess, docIdx01, scoresOut1[0])
				guess = ctx.Scoring.Add(guess, docIdx02, scoresOut1[1])
				guess = ctx.Scoring.Add(guess, docIdx03, scoresOut1[2])
				guess = ctx.Scoring.Add(guess, docIdx04, scoresOut1[3])
				guess = ctx.Scoring.Add(guess, docIdx05, scoresOut1[4])
				guess = ctx.Scoring.Add(guess, docIdx06, scoresOut1[5])
				guess = ctx.Scoring.Add(guess, docIdx07, scoresOut1[6])
				guess = ctx.Scoring.Add(guess, docIdx08, scoresOut1[7])

				guess = ctx.Scoring.Add(guess, docIdx09, scoresOut2[0])
				guess = ctx.Scoring.Add(guess, docIdx10, scoresOut2[1])
				guess = ctx.Scoring.Add(guess, docIdx11, scoresOut2[2])
				guess = ctx.Scoring.Add(guess, docIdx12, scoresOut2[3])
				guess = ctx.Scoring.Add(guess, docIdx13, scoresOut2[4])
				guess = ctx.Scoring.Add(guess, docIdx14, scoresOut2[5])
				guess = ctx.Scoring.Add(guess, docIdx15, scoresOut2[6])
				guess = ctx.Scoring.Add(guess, docIdx16, scoresOut2[7])

				guess = ctx.Scoring.Add(guess, docIdx17, scoresOut3[0])
				guess = ctx.Scoring.Add(guess, docIdx18, scoresOut3[1])
				guess = ctx.Scoring.Add(guess, docIdx19, scoresOut3[2])
				guess = ctx.Scoring.Add(guess, docIdx20, scoresOut3[3])
				guess = ctx.Scoring.Add(guess, docIdx21, scoresOut3[4])
				guess = ctx.Scoring.Add(guess, docIdx22, scoresOut3[5])
				guess = ctx.Scoring.Add(guess, docIdx23, scoresOut3[6])
				guess = ctx.Scoring.Add(guess, docIdx24, scoresOut3[7])

				guess = ctx.Scoring.Add(guess, docIdx25, scoresOut4[0])
				guess = ctx.Scoring.Add(guess, docIdx26, scoresOut4[1])
				guess = ctx.Scoring.Add(guess, docIdx27, scoresOut4[2])
				guess = ctx.Scoring.Add(guess, docIdx28, scoresOut4[3])
				guess = ctx.Scoring.Add(guess, docIdx29, scoresOut4[4])
				guess = ctx.Scoring.Add(guess, docIdx30, scoresOut4[5])
				guess = ctx.Scoring.Add(guess, docIdx31, scoresOut4[6])
				guess = ctx.Scoring.Add(guess, docIdx32, scoresOut4[7])

				guess = ctx.Scoring.Add(guess, docIdx33, scoresOut5[0])
				guess = ctx.Scoring.Add(guess, docIdx34, scoresOut5[1])
				guess = ctx.Scoring.Add(guess, docIdx35, scoresOut5[2])
				guess = ctx.Scoring.Add(guess, docIdx36, scoresOut5[3])
				guess = ctx.Scoring.Add(guess, docIdx37, scoresOut5[4])
				guess = ctx.Scoring.Add(guess, docIdx38, scoresOut5[5])
				guess = ctx.Scoring.Add(guess, docIdx39, scoresOut5[6])
				guess = ctx.Scoring.Add(guess, docIdx40, scoresOut5[7])

				guess = ctx.Scoring.Add(guess, docIdx41, scoresOut6[0])
				guess = ctx.Scoring.Add(guess, docIdx42, scoresOut6[1])
				guess = ctx.Scoring.Add(guess, docIdx43, scoresOut6[2])
				guess = ctx.Scoring.Add(guess, docIdx44, scoresOut6[3])
				guess = ctx.Scoring.Add(guess, docIdx45, scoresOut6[4])
				guess = ctx.Scoring.Add(guess, docIdx46, scoresOut6[5])
				guess = ctx.Scoring.Add(guess, docIdx47, scoresOut6[6])
				guess = ctx.Scoring.Add(guess, docIdx48, scoresOut6[7])

				guess = ctx.Scoring.Add(guess, docIdx49, scoresOut7[0])
				guess = ctx.Scoring.Add(guess, docIdx50, scoresOut7[1])
				guess = ctx.Scoring.Add(guess, docIdx51, scoresOut7[2])
				guess = ctx.Scoring.Add(guess, docIdx52, scoresOut7[3])
				guess = ctx.Scoring.Add(guess, docIdx53, scoresOut7[4])
				guess = ctx.Scoring.Add(guess, docIdx54, scoresOut7[5])
				guess = ctx.Scoring.Add(guess, docIdx55, scoresOut7[6])
				guess = ctx.Scoring.Add(guess, docIdx56, scoresOut7[7])

				guess = ctx.Scoring.Add(guess, docIdx57, scoresOut8[0])
				guess = ctx.Scoring.Add(guess, docIdx58, scoresOut8[1])
				guess = ctx.Scoring.Add(guess, docIdx59, scoresOut8[2])
				guess = ctx.Scoring.Add(guess, docIdx60, scoresOut8[3])
				guess = ctx.Scoring.Add(guess, docIdx61, scoresOut8[4])
				guess = ctx.Scoring.Add(guess, docIdx62, scoresOut8[5])
				guess = ctx.Scoring.Add(guess, docIdx63, scoresOut8[6])
				guess = ctx.Scoring.Add(guess, docIdx64, scoresOut8[7])
			}

			// Remainder that still fills one whole vector.
			for i := n64; i < n8; i += LaneCountAvx2 {
				docIdx01, docIdx02, docIdx03, docIdx04 := resolved[0+i], resolved[1+i], resolved[2+i], resolved[3+i]
				docIdx05, docIdx06, docIdx07, docIdx08 := resolved[4+i], resolved[5+i], resolved[6+i], resolved[7+i]

				// Sparse document lengths: descending cascade, each probe narrows the next.
				docLengthIdx08, _ := slices.BinarySearchFunc(docLengths, docIdx08, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx07, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx08], docIdx07, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx06, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx07], docIdx06, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx05, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx06], docIdx05, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx04, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx05], docIdx04, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx03, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx04], docIdx03, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx02, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx03], docIdx02, CmpDocumentLengthEntryAndDocumentIndex)
				docLengthIdx01, _ := slices.BinarySearchFunc(docLengths[:docLengthIdx02], docIdx01, CmpDocumentLengthEntryAndDocumentIndex)

				dlsVec1[0], dlsVec1[1] = float32(docLengths[docLengthIdx01].Length), float32(docLengths[docLengthIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docLengthIdx03].Length), float32(docLengths[docLengthIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docLengthIdx05].Length), float32(docLengths[docLengthIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docLengthIdx07].Length), float32(docLengths[docLengthIdx08].Length)
				docLengths = docLengths[1+docLengthIdx08:]

				// Sparse token frequencies: descending cascade, each probe narrows the next.
				freqIdx08, _ := slices.BinarySearchFunc(freqs, docIdx08, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx07, _ := slices.BinarySearchFunc(freqs[:freqIdx08], docIdx07, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx06, _ := slices.BinarySearchFunc(freqs[:freqIdx07], docIdx06, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx05, _ := slices.BinarySearchFunc(freqs[:freqIdx06], docIdx05, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx04, _ := slices.BinarySearchFunc(freqs[:freqIdx05], docIdx04, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx03, _ := slices.BinarySearchFunc(freqs[:freqIdx04], docIdx03, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx02, _ := slices.BinarySearchFunc(freqs[:freqIdx03], docIdx02, CmpTokenFrequencyEntryAndDocumentIndex)
				freqIdx01, _ := slices.BinarySearchFunc(freqs[:freqIdx02], docIdx01, CmpTokenFrequencyEntryAndDocumentIndex)

				tfsVec1[0], tfsVec1[1] = float32(freqs[freqIdx01].Frequency), float32(freqs[freqIdx02].Frequency)
				tfsVec1[2], tfsVec1[3] = float32(freqs[freqIdx03].Frequency), float32(freqs[freqIdx04].Frequency)
				tfsVec1[4], tfsVec1[5] = float32(freqs[freqIdx05].Frequency), float32(freqs[freqIdx06].Frequency)
				tfsVec1[6], tfsVec1[7] = float32(freqs[freqIdx07].Frequency), float32(freqs[freqIdx08].Frequency)
				freqs = freqs[1+freqIdx08:]

				dls1 := archsimd.LoadFloat32x8Array(&dlsVec1)
				tfs1 := archsimd.LoadFloat32x8Array(&tfsVec1)
				partial1 := tfs1.Add(satXoneMinuxLps)
				denominator1 := dls1.MulAdd(saturationXLengthPenaltyDivAvgDocLengths, partial1)
				reciprocal1 := denominator1.Reciprocal()
				tfNorms1 := tfs1.Mul(reciprocal1)
				scores1 := idfBoosts.Mul(tfNorms1)
				scores1.StoreArray(&scoresOut1)

				guess = ctx.Scoring.Add(guess, docIdx01, scoresOut1[0])
				guess = ctx.Scoring.Add(guess, docIdx02, scoresOut1[1])
				guess = ctx.Scoring.Add(guess, docIdx03, scoresOut1[2])
				guess = ctx.Scoring.Add(guess, docIdx04, scoresOut1[3])
				guess = ctx.Scoring.Add(guess, docIdx05, scoresOut1[4])
				guess = ctx.Scoring.Add(guess, docIdx06, scoresOut1[5])
				guess = ctx.Scoring.Add(guess, docIdx07, scoresOut1[6])
				guess = ctx.Scoring.Add(guess, docIdx08, scoresOut1[7])
			}

			// Final < 8 documents, scalar.
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
