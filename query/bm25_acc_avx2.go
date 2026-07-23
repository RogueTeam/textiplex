package query

import (
	"simd/archsimd"

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
				docLengthIdx64, _ := docLengths.BinarySearch(docIdx64)
				docLengthIdx63, _ := docLengths[:docLengthIdx64].BinarySearch(docIdx63)
				docLengthIdx62, _ := docLengths[:docLengthIdx63].BinarySearch(docIdx62)
				docLengthIdx61, _ := docLengths[:docLengthIdx62].BinarySearch(docIdx61)
				docLengthIdx60, _ := docLengths[:docLengthIdx61].BinarySearch(docIdx60)
				docLengthIdx59, _ := docLengths[:docLengthIdx60].BinarySearch(docIdx59)
				docLengthIdx58, _ := docLengths[:docLengthIdx59].BinarySearch(docIdx58)
				docLengthIdx57, _ := docLengths[:docLengthIdx58].BinarySearch(docIdx57)
				docLengthIdx56, _ := docLengths[:docLengthIdx57].BinarySearch(docIdx56)
				docLengthIdx55, _ := docLengths[:docLengthIdx56].BinarySearch(docIdx55)
				docLengthIdx54, _ := docLengths[:docLengthIdx55].BinarySearch(docIdx54)
				docLengthIdx53, _ := docLengths[:docLengthIdx54].BinarySearch(docIdx53)
				docLengthIdx52, _ := docLengths[:docLengthIdx53].BinarySearch(docIdx52)
				docLengthIdx51, _ := docLengths[:docLengthIdx52].BinarySearch(docIdx51)
				docLengthIdx50, _ := docLengths[:docLengthIdx51].BinarySearch(docIdx50)
				docLengthIdx49, _ := docLengths[:docLengthIdx50].BinarySearch(docIdx49)
				docLengthIdx48, _ := docLengths[:docLengthIdx49].BinarySearch(docIdx48)
				docLengthIdx47, _ := docLengths[:docLengthIdx48].BinarySearch(docIdx47)
				docLengthIdx46, _ := docLengths[:docLengthIdx47].BinarySearch(docIdx46)
				docLengthIdx45, _ := docLengths[:docLengthIdx46].BinarySearch(docIdx45)
				docLengthIdx44, _ := docLengths[:docLengthIdx45].BinarySearch(docIdx44)
				docLengthIdx43, _ := docLengths[:docLengthIdx44].BinarySearch(docIdx43)
				docLengthIdx42, _ := docLengths[:docLengthIdx43].BinarySearch(docIdx42)
				docLengthIdx41, _ := docLengths[:docLengthIdx42].BinarySearch(docIdx41)
				docLengthIdx40, _ := docLengths[:docLengthIdx41].BinarySearch(docIdx40)
				docLengthIdx39, _ := docLengths[:docLengthIdx40].BinarySearch(docIdx39)
				docLengthIdx38, _ := docLengths[:docLengthIdx39].BinarySearch(docIdx38)
				docLengthIdx37, _ := docLengths[:docLengthIdx38].BinarySearch(docIdx37)
				docLengthIdx36, _ := docLengths[:docLengthIdx37].BinarySearch(docIdx36)
				docLengthIdx35, _ := docLengths[:docLengthIdx36].BinarySearch(docIdx35)
				docLengthIdx34, _ := docLengths[:docLengthIdx35].BinarySearch(docIdx34)
				docLengthIdx33, _ := docLengths[:docLengthIdx34].BinarySearch(docIdx33)
				docLengthIdx32, _ := docLengths[:docLengthIdx33].BinarySearch(docIdx32)
				docLengthIdx31, _ := docLengths[:docLengthIdx32].BinarySearch(docIdx31)
				docLengthIdx30, _ := docLengths[:docLengthIdx31].BinarySearch(docIdx30)
				docLengthIdx29, _ := docLengths[:docLengthIdx30].BinarySearch(docIdx29)
				docLengthIdx28, _ := docLengths[:docLengthIdx29].BinarySearch(docIdx28)
				docLengthIdx27, _ := docLengths[:docLengthIdx28].BinarySearch(docIdx27)
				docLengthIdx26, _ := docLengths[:docLengthIdx27].BinarySearch(docIdx26)
				docLengthIdx25, _ := docLengths[:docLengthIdx26].BinarySearch(docIdx25)
				docLengthIdx24, _ := docLengths[:docLengthIdx25].BinarySearch(docIdx24)
				docLengthIdx23, _ := docLengths[:docLengthIdx24].BinarySearch(docIdx23)
				docLengthIdx22, _ := docLengths[:docLengthIdx23].BinarySearch(docIdx22)
				docLengthIdx21, _ := docLengths[:docLengthIdx22].BinarySearch(docIdx21)
				docLengthIdx20, _ := docLengths[:docLengthIdx21].BinarySearch(docIdx20)
				docLengthIdx19, _ := docLengths[:docLengthIdx20].BinarySearch(docIdx19)
				docLengthIdx18, _ := docLengths[:docLengthIdx19].BinarySearch(docIdx18)
				docLengthIdx17, _ := docLengths[:docLengthIdx18].BinarySearch(docIdx17)
				docLengthIdx16, _ := docLengths[:docLengthIdx17].BinarySearch(docIdx16)
				docLengthIdx15, _ := docLengths[:docLengthIdx16].BinarySearch(docIdx15)
				docLengthIdx14, _ := docLengths[:docLengthIdx15].BinarySearch(docIdx14)
				docLengthIdx13, _ := docLengths[:docLengthIdx14].BinarySearch(docIdx13)
				docLengthIdx12, _ := docLengths[:docLengthIdx13].BinarySearch(docIdx12)
				docLengthIdx11, _ := docLengths[:docLengthIdx12].BinarySearch(docIdx11)
				docLengthIdx10, _ := docLengths[:docLengthIdx11].BinarySearch(docIdx10)
				docLengthIdx09, _ := docLengths[:docLengthIdx10].BinarySearch(docIdx09)
				docLengthIdx08, _ := docLengths[:docLengthIdx09].BinarySearch(docIdx08)
				docLengthIdx07, _ := docLengths[:docLengthIdx08].BinarySearch(docIdx07)
				docLengthIdx06, _ := docLengths[:docLengthIdx07].BinarySearch(docIdx06)
				docLengthIdx05, _ := docLengths[:docLengthIdx06].BinarySearch(docIdx05)
				docLengthIdx04, _ := docLengths[:docLengthIdx05].BinarySearch(docIdx04)
				docLengthIdx03, _ := docLengths[:docLengthIdx04].BinarySearch(docIdx03)
				docLengthIdx02, _ := docLengths[:docLengthIdx03].BinarySearch(docIdx02)
				docLengthIdx01, _ := docLengths[:docLengthIdx02].BinarySearch(docIdx01)

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
				docLengthIdx08, _ := docLengths.BinarySearch(docIdx08)
				docLengthIdx07, _ := docLengths[:docLengthIdx08].BinarySearch(docIdx07)
				docLengthIdx06, _ := docLengths[:docLengthIdx07].BinarySearch(docIdx06)
				docLengthIdx05, _ := docLengths[:docLengthIdx06].BinarySearch(docIdx05)
				docLengthIdx04, _ := docLengths[:docLengthIdx05].BinarySearch(docIdx04)
				docLengthIdx03, _ := docLengths[:docLengthIdx04].BinarySearch(docIdx03)
				docLengthIdx02, _ := docLengths[:docLengthIdx03].BinarySearch(docIdx02)
				docLengthIdx01, _ := docLengths[:docLengthIdx02].BinarySearch(docIdx01)

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
				docLengthIdx, _ := docLengths.BinarySearch(docIdx)

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
				freqIdx64, _ := freqs.BinarySearch(docIdx64)
				freqIdx63, _ := freqs[:freqIdx64].BinarySearch(docIdx63)
				freqIdx62, _ := freqs[:freqIdx63].BinarySearch(docIdx62)
				freqIdx61, _ := freqs[:freqIdx62].BinarySearch(docIdx61)
				freqIdx60, _ := freqs[:freqIdx61].BinarySearch(docIdx60)
				freqIdx59, _ := freqs[:freqIdx60].BinarySearch(docIdx59)
				freqIdx58, _ := freqs[:freqIdx59].BinarySearch(docIdx58)
				freqIdx57, _ := freqs[:freqIdx58].BinarySearch(docIdx57)
				freqIdx56, _ := freqs[:freqIdx57].BinarySearch(docIdx56)
				freqIdx55, _ := freqs[:freqIdx56].BinarySearch(docIdx55)
				freqIdx54, _ := freqs[:freqIdx55].BinarySearch(docIdx54)
				freqIdx53, _ := freqs[:freqIdx54].BinarySearch(docIdx53)
				freqIdx52, _ := freqs[:freqIdx53].BinarySearch(docIdx52)
				freqIdx51, _ := freqs[:freqIdx52].BinarySearch(docIdx51)
				freqIdx50, _ := freqs[:freqIdx51].BinarySearch(docIdx50)
				freqIdx49, _ := freqs[:freqIdx50].BinarySearch(docIdx49)
				freqIdx48, _ := freqs[:freqIdx49].BinarySearch(docIdx48)
				freqIdx47, _ := freqs[:freqIdx48].BinarySearch(docIdx47)
				freqIdx46, _ := freqs[:freqIdx47].BinarySearch(docIdx46)
				freqIdx45, _ := freqs[:freqIdx46].BinarySearch(docIdx45)
				freqIdx44, _ := freqs[:freqIdx45].BinarySearch(docIdx44)
				freqIdx43, _ := freqs[:freqIdx44].BinarySearch(docIdx43)
				freqIdx42, _ := freqs[:freqIdx43].BinarySearch(docIdx42)
				freqIdx41, _ := freqs[:freqIdx42].BinarySearch(docIdx41)
				freqIdx40, _ := freqs[:freqIdx41].BinarySearch(docIdx40)
				freqIdx39, _ := freqs[:freqIdx40].BinarySearch(docIdx39)
				freqIdx38, _ := freqs[:freqIdx39].BinarySearch(docIdx38)
				freqIdx37, _ := freqs[:freqIdx38].BinarySearch(docIdx37)
				freqIdx36, _ := freqs[:freqIdx37].BinarySearch(docIdx36)
				freqIdx35, _ := freqs[:freqIdx36].BinarySearch(docIdx35)
				freqIdx34, _ := freqs[:freqIdx35].BinarySearch(docIdx34)
				freqIdx33, _ := freqs[:freqIdx34].BinarySearch(docIdx33)
				freqIdx32, _ := freqs[:freqIdx33].BinarySearch(docIdx32)
				freqIdx31, _ := freqs[:freqIdx32].BinarySearch(docIdx31)
				freqIdx30, _ := freqs[:freqIdx31].BinarySearch(docIdx30)
				freqIdx29, _ := freqs[:freqIdx30].BinarySearch(docIdx29)
				freqIdx28, _ := freqs[:freqIdx29].BinarySearch(docIdx28)
				freqIdx27, _ := freqs[:freqIdx28].BinarySearch(docIdx27)
				freqIdx26, _ := freqs[:freqIdx27].BinarySearch(docIdx26)
				freqIdx25, _ := freqs[:freqIdx26].BinarySearch(docIdx25)
				freqIdx24, _ := freqs[:freqIdx25].BinarySearch(docIdx24)
				freqIdx23, _ := freqs[:freqIdx24].BinarySearch(docIdx23)
				freqIdx22, _ := freqs[:freqIdx23].BinarySearch(docIdx22)
				freqIdx21, _ := freqs[:freqIdx22].BinarySearch(docIdx21)
				freqIdx20, _ := freqs[:freqIdx21].BinarySearch(docIdx20)
				freqIdx19, _ := freqs[:freqIdx20].BinarySearch(docIdx19)
				freqIdx18, _ := freqs[:freqIdx19].BinarySearch(docIdx18)
				freqIdx17, _ := freqs[:freqIdx18].BinarySearch(docIdx17)
				freqIdx16, _ := freqs[:freqIdx17].BinarySearch(docIdx16)
				freqIdx15, _ := freqs[:freqIdx16].BinarySearch(docIdx15)
				freqIdx14, _ := freqs[:freqIdx15].BinarySearch(docIdx14)
				freqIdx13, _ := freqs[:freqIdx14].BinarySearch(docIdx13)
				freqIdx12, _ := freqs[:freqIdx13].BinarySearch(docIdx12)
				freqIdx11, _ := freqs[:freqIdx12].BinarySearch(docIdx11)
				freqIdx10, _ := freqs[:freqIdx11].BinarySearch(docIdx10)
				freqIdx09, _ := freqs[:freqIdx10].BinarySearch(docIdx09)
				freqIdx08, _ := freqs[:freqIdx09].BinarySearch(docIdx08)
				freqIdx07, _ := freqs[:freqIdx08].BinarySearch(docIdx07)
				freqIdx06, _ := freqs[:freqIdx07].BinarySearch(docIdx06)
				freqIdx05, _ := freqs[:freqIdx06].BinarySearch(docIdx05)
				freqIdx04, _ := freqs[:freqIdx05].BinarySearch(docIdx04)
				freqIdx03, _ := freqs[:freqIdx04].BinarySearch(docIdx03)
				freqIdx02, _ := freqs[:freqIdx03].BinarySearch(docIdx02)
				freqIdx01, _ := freqs[:freqIdx02].BinarySearch(docIdx01)

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
				freqIdx08, _ := freqs.BinarySearch(docIdx08)
				freqIdx07, _ := freqs[:freqIdx08].BinarySearch(docIdx07)
				freqIdx06, _ := freqs[:freqIdx07].BinarySearch(docIdx06)
				freqIdx05, _ := freqs[:freqIdx06].BinarySearch(docIdx05)
				freqIdx04, _ := freqs[:freqIdx05].BinarySearch(docIdx04)
				freqIdx03, _ := freqs[:freqIdx04].BinarySearch(docIdx03)
				freqIdx02, _ := freqs[:freqIdx03].BinarySearch(docIdx02)
				freqIdx01, _ := freqs[:freqIdx02].BinarySearch(docIdx01)

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
				freqIdx, _ := freqs.BinarySearch(docIdx)

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
				docLengthIdx64, _ := docLengths.BinarySearch(docIdx64)
				docLengthIdx63, _ := docLengths[:docLengthIdx64].BinarySearch(docIdx63)
				docLengthIdx62, _ := docLengths[:docLengthIdx63].BinarySearch(docIdx62)
				docLengthIdx61, _ := docLengths[:docLengthIdx62].BinarySearch(docIdx61)
				docLengthIdx60, _ := docLengths[:docLengthIdx61].BinarySearch(docIdx60)
				docLengthIdx59, _ := docLengths[:docLengthIdx60].BinarySearch(docIdx59)
				docLengthIdx58, _ := docLengths[:docLengthIdx59].BinarySearch(docIdx58)
				docLengthIdx57, _ := docLengths[:docLengthIdx58].BinarySearch(docIdx57)
				docLengthIdx56, _ := docLengths[:docLengthIdx57].BinarySearch(docIdx56)
				docLengthIdx55, _ := docLengths[:docLengthIdx56].BinarySearch(docIdx55)
				docLengthIdx54, _ := docLengths[:docLengthIdx55].BinarySearch(docIdx54)
				docLengthIdx53, _ := docLengths[:docLengthIdx54].BinarySearch(docIdx53)
				docLengthIdx52, _ := docLengths[:docLengthIdx53].BinarySearch(docIdx52)
				docLengthIdx51, _ := docLengths[:docLengthIdx52].BinarySearch(docIdx51)
				docLengthIdx50, _ := docLengths[:docLengthIdx51].BinarySearch(docIdx50)
				docLengthIdx49, _ := docLengths[:docLengthIdx50].BinarySearch(docIdx49)
				docLengthIdx48, _ := docLengths[:docLengthIdx49].BinarySearch(docIdx48)
				docLengthIdx47, _ := docLengths[:docLengthIdx48].BinarySearch(docIdx47)
				docLengthIdx46, _ := docLengths[:docLengthIdx47].BinarySearch(docIdx46)
				docLengthIdx45, _ := docLengths[:docLengthIdx46].BinarySearch(docIdx45)
				docLengthIdx44, _ := docLengths[:docLengthIdx45].BinarySearch(docIdx44)
				docLengthIdx43, _ := docLengths[:docLengthIdx44].BinarySearch(docIdx43)
				docLengthIdx42, _ := docLengths[:docLengthIdx43].BinarySearch(docIdx42)
				docLengthIdx41, _ := docLengths[:docLengthIdx42].BinarySearch(docIdx41)
				docLengthIdx40, _ := docLengths[:docLengthIdx41].BinarySearch(docIdx40)
				docLengthIdx39, _ := docLengths[:docLengthIdx40].BinarySearch(docIdx39)
				docLengthIdx38, _ := docLengths[:docLengthIdx39].BinarySearch(docIdx38)
				docLengthIdx37, _ := docLengths[:docLengthIdx38].BinarySearch(docIdx37)
				docLengthIdx36, _ := docLengths[:docLengthIdx37].BinarySearch(docIdx36)
				docLengthIdx35, _ := docLengths[:docLengthIdx36].BinarySearch(docIdx35)
				docLengthIdx34, _ := docLengths[:docLengthIdx35].BinarySearch(docIdx34)
				docLengthIdx33, _ := docLengths[:docLengthIdx34].BinarySearch(docIdx33)
				docLengthIdx32, _ := docLengths[:docLengthIdx33].BinarySearch(docIdx32)
				docLengthIdx31, _ := docLengths[:docLengthIdx32].BinarySearch(docIdx31)
				docLengthIdx30, _ := docLengths[:docLengthIdx31].BinarySearch(docIdx30)
				docLengthIdx29, _ := docLengths[:docLengthIdx30].BinarySearch(docIdx29)
				docLengthIdx28, _ := docLengths[:docLengthIdx29].BinarySearch(docIdx28)
				docLengthIdx27, _ := docLengths[:docLengthIdx28].BinarySearch(docIdx27)
				docLengthIdx26, _ := docLengths[:docLengthIdx27].BinarySearch(docIdx26)
				docLengthIdx25, _ := docLengths[:docLengthIdx26].BinarySearch(docIdx25)
				docLengthIdx24, _ := docLengths[:docLengthIdx25].BinarySearch(docIdx24)
				docLengthIdx23, _ := docLengths[:docLengthIdx24].BinarySearch(docIdx23)
				docLengthIdx22, _ := docLengths[:docLengthIdx23].BinarySearch(docIdx22)
				docLengthIdx21, _ := docLengths[:docLengthIdx22].BinarySearch(docIdx21)
				docLengthIdx20, _ := docLengths[:docLengthIdx21].BinarySearch(docIdx20)
				docLengthIdx19, _ := docLengths[:docLengthIdx20].BinarySearch(docIdx19)
				docLengthIdx18, _ := docLengths[:docLengthIdx19].BinarySearch(docIdx18)
				docLengthIdx17, _ := docLengths[:docLengthIdx18].BinarySearch(docIdx17)
				docLengthIdx16, _ := docLengths[:docLengthIdx17].BinarySearch(docIdx16)
				docLengthIdx15, _ := docLengths[:docLengthIdx16].BinarySearch(docIdx15)
				docLengthIdx14, _ := docLengths[:docLengthIdx15].BinarySearch(docIdx14)
				docLengthIdx13, _ := docLengths[:docLengthIdx14].BinarySearch(docIdx13)
				docLengthIdx12, _ := docLengths[:docLengthIdx13].BinarySearch(docIdx12)
				docLengthIdx11, _ := docLengths[:docLengthIdx12].BinarySearch(docIdx11)
				docLengthIdx10, _ := docLengths[:docLengthIdx11].BinarySearch(docIdx10)
				docLengthIdx09, _ := docLengths[:docLengthIdx10].BinarySearch(docIdx09)
				docLengthIdx08, _ := docLengths[:docLengthIdx09].BinarySearch(docIdx08)
				docLengthIdx07, _ := docLengths[:docLengthIdx08].BinarySearch(docIdx07)
				docLengthIdx06, _ := docLengths[:docLengthIdx07].BinarySearch(docIdx06)
				docLengthIdx05, _ := docLengths[:docLengthIdx06].BinarySearch(docIdx05)
				docLengthIdx04, _ := docLengths[:docLengthIdx05].BinarySearch(docIdx04)
				docLengthIdx03, _ := docLengths[:docLengthIdx04].BinarySearch(docIdx03)
				docLengthIdx02, _ := docLengths[:docLengthIdx03].BinarySearch(docIdx02)
				docLengthIdx01, _ := docLengths[:docLengthIdx02].BinarySearch(docIdx01)

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
				freqIdx64, _ := freqs.BinarySearch(docIdx64)
				freqIdx63, _ := freqs[:freqIdx64].BinarySearch(docIdx63)
				freqIdx62, _ := freqs[:freqIdx63].BinarySearch(docIdx62)
				freqIdx61, _ := freqs[:freqIdx62].BinarySearch(docIdx61)
				freqIdx60, _ := freqs[:freqIdx61].BinarySearch(docIdx60)
				freqIdx59, _ := freqs[:freqIdx60].BinarySearch(docIdx59)
				freqIdx58, _ := freqs[:freqIdx59].BinarySearch(docIdx58)
				freqIdx57, _ := freqs[:freqIdx58].BinarySearch(docIdx57)
				freqIdx56, _ := freqs[:freqIdx57].BinarySearch(docIdx56)
				freqIdx55, _ := freqs[:freqIdx56].BinarySearch(docIdx55)
				freqIdx54, _ := freqs[:freqIdx55].BinarySearch(docIdx54)
				freqIdx53, _ := freqs[:freqIdx54].BinarySearch(docIdx53)
				freqIdx52, _ := freqs[:freqIdx53].BinarySearch(docIdx52)
				freqIdx51, _ := freqs[:freqIdx52].BinarySearch(docIdx51)
				freqIdx50, _ := freqs[:freqIdx51].BinarySearch(docIdx50)
				freqIdx49, _ := freqs[:freqIdx50].BinarySearch(docIdx49)
				freqIdx48, _ := freqs[:freqIdx49].BinarySearch(docIdx48)
				freqIdx47, _ := freqs[:freqIdx48].BinarySearch(docIdx47)
				freqIdx46, _ := freqs[:freqIdx47].BinarySearch(docIdx46)
				freqIdx45, _ := freqs[:freqIdx46].BinarySearch(docIdx45)
				freqIdx44, _ := freqs[:freqIdx45].BinarySearch(docIdx44)
				freqIdx43, _ := freqs[:freqIdx44].BinarySearch(docIdx43)
				freqIdx42, _ := freqs[:freqIdx43].BinarySearch(docIdx42)
				freqIdx41, _ := freqs[:freqIdx42].BinarySearch(docIdx41)
				freqIdx40, _ := freqs[:freqIdx41].BinarySearch(docIdx40)
				freqIdx39, _ := freqs[:freqIdx40].BinarySearch(docIdx39)
				freqIdx38, _ := freqs[:freqIdx39].BinarySearch(docIdx38)
				freqIdx37, _ := freqs[:freqIdx38].BinarySearch(docIdx37)
				freqIdx36, _ := freqs[:freqIdx37].BinarySearch(docIdx36)
				freqIdx35, _ := freqs[:freqIdx36].BinarySearch(docIdx35)
				freqIdx34, _ := freqs[:freqIdx35].BinarySearch(docIdx34)
				freqIdx33, _ := freqs[:freqIdx34].BinarySearch(docIdx33)
				freqIdx32, _ := freqs[:freqIdx33].BinarySearch(docIdx32)
				freqIdx31, _ := freqs[:freqIdx32].BinarySearch(docIdx31)
				freqIdx30, _ := freqs[:freqIdx31].BinarySearch(docIdx30)
				freqIdx29, _ := freqs[:freqIdx30].BinarySearch(docIdx29)
				freqIdx28, _ := freqs[:freqIdx29].BinarySearch(docIdx28)
				freqIdx27, _ := freqs[:freqIdx28].BinarySearch(docIdx27)
				freqIdx26, _ := freqs[:freqIdx27].BinarySearch(docIdx26)
				freqIdx25, _ := freqs[:freqIdx26].BinarySearch(docIdx25)
				freqIdx24, _ := freqs[:freqIdx25].BinarySearch(docIdx24)
				freqIdx23, _ := freqs[:freqIdx24].BinarySearch(docIdx23)
				freqIdx22, _ := freqs[:freqIdx23].BinarySearch(docIdx22)
				freqIdx21, _ := freqs[:freqIdx22].BinarySearch(docIdx21)
				freqIdx20, _ := freqs[:freqIdx21].BinarySearch(docIdx20)
				freqIdx19, _ := freqs[:freqIdx20].BinarySearch(docIdx19)
				freqIdx18, _ := freqs[:freqIdx19].BinarySearch(docIdx18)
				freqIdx17, _ := freqs[:freqIdx18].BinarySearch(docIdx17)
				freqIdx16, _ := freqs[:freqIdx17].BinarySearch(docIdx16)
				freqIdx15, _ := freqs[:freqIdx16].BinarySearch(docIdx15)
				freqIdx14, _ := freqs[:freqIdx15].BinarySearch(docIdx14)
				freqIdx13, _ := freqs[:freqIdx14].BinarySearch(docIdx13)
				freqIdx12, _ := freqs[:freqIdx13].BinarySearch(docIdx12)
				freqIdx11, _ := freqs[:freqIdx12].BinarySearch(docIdx11)
				freqIdx10, _ := freqs[:freqIdx11].BinarySearch(docIdx10)
				freqIdx09, _ := freqs[:freqIdx10].BinarySearch(docIdx09)
				freqIdx08, _ := freqs[:freqIdx09].BinarySearch(docIdx08)
				freqIdx07, _ := freqs[:freqIdx08].BinarySearch(docIdx07)
				freqIdx06, _ := freqs[:freqIdx07].BinarySearch(docIdx06)
				freqIdx05, _ := freqs[:freqIdx06].BinarySearch(docIdx05)
				freqIdx04, _ := freqs[:freqIdx05].BinarySearch(docIdx04)
				freqIdx03, _ := freqs[:freqIdx04].BinarySearch(docIdx03)
				freqIdx02, _ := freqs[:freqIdx03].BinarySearch(docIdx02)
				freqIdx01, _ := freqs[:freqIdx02].BinarySearch(docIdx01)

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
				docLengthIdx08, _ := docLengths.BinarySearch(docIdx08)
				docLengthIdx07, _ := docLengths[:docLengthIdx08].BinarySearch(docIdx07)
				docLengthIdx06, _ := docLengths[:docLengthIdx07].BinarySearch(docIdx06)
				docLengthIdx05, _ := docLengths[:docLengthIdx06].BinarySearch(docIdx05)
				docLengthIdx04, _ := docLengths[:docLengthIdx05].BinarySearch(docIdx04)
				docLengthIdx03, _ := docLengths[:docLengthIdx04].BinarySearch(docIdx03)
				docLengthIdx02, _ := docLengths[:docLengthIdx03].BinarySearch(docIdx02)
				docLengthIdx01, _ := docLengths[:docLengthIdx02].BinarySearch(docIdx01)

				dlsVec1[0], dlsVec1[1] = float32(docLengths[docLengthIdx01].Length), float32(docLengths[docLengthIdx02].Length)
				dlsVec1[2], dlsVec1[3] = float32(docLengths[docLengthIdx03].Length), float32(docLengths[docLengthIdx04].Length)
				dlsVec1[4], dlsVec1[5] = float32(docLengths[docLengthIdx05].Length), float32(docLengths[docLengthIdx06].Length)
				dlsVec1[6], dlsVec1[7] = float32(docLengths[docLengthIdx07].Length), float32(docLengths[docLengthIdx08].Length)
				docLengths = docLengths[1+docLengthIdx08:]

				// Sparse token frequencies: descending cascade, each probe narrows the next.
				freqIdx08, _ := freqs.BinarySearch(docIdx08)
				freqIdx07, _ := freqs[:freqIdx08].BinarySearch(docIdx07)
				freqIdx06, _ := freqs[:freqIdx07].BinarySearch(docIdx06)
				freqIdx05, _ := freqs[:freqIdx06].BinarySearch(docIdx05)
				freqIdx04, _ := freqs[:freqIdx05].BinarySearch(docIdx04)
				freqIdx03, _ := freqs[:freqIdx04].BinarySearch(docIdx03)
				freqIdx02, _ := freqs[:freqIdx03].BinarySearch(docIdx02)
				freqIdx01, _ := freqs[:freqIdx02].BinarySearch(docIdx01)

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

				freqIdx, _ := freqs.BinarySearch(docIdx)
				docLengthIdx, _ := docLengths.BinarySearch(docIdx)

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
