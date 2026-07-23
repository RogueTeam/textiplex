package query

import (
	"simd/archsimd"
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
			s.AccumulateBM25(ctx, state, saturation, lengthPenalty)
		})
	}
	if q.Shoulds.Count() > 0 {
		s.Iter(&q.Shoulds, func(state *ClauseState) {
			s.AccumulateBM25(ctx, state, saturation, lengthPenalty)
		})
	}
}

func (s *Searcher) AccumulateBM25(ctx *QueryContext, state *ClauseState, saturation, lengthPenalty float32) {
	switch {
	case !(s.ForceScalar || ForceScalar) && archsimd.X86.AVX2():
		s.AVX2AccumulateBM25(ctx, state, saturation, lengthPenalty)
	default:
		s.ScalarAccumulateBM25(ctx, state, saturation, lengthPenalty)
	}
}
