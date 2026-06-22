package storage

import (
	"simd/archsimd"

	"github.com/RoaringBitmap/roaring/roaring64"
)

var addVector func(dst *[8]uint64, vec *[8]uint64)

func init() {
	switch {
	case archsimd.X86.AVX512():
		addVector = func(dst, vec *[8]uint64) {
			archsimd.LoadUint64x8(dst).
				Add(archsimd.LoadUint64x8(vec)).
				Store(dst)
		}
	case archsimd.X86.AVX2():
		addVector = func(dst, vec *[8]uint64) {

			archsimd.LoadUint64x4((*[4]uint64)(dst[0:])).
				Add(archsimd.LoadUint64x4((*[4]uint64)(vec[0:]))).
				Store((*[4]uint64)(dst[0:]))

			archsimd.LoadUint64x4((*[4]uint64)(dst[4:])).
				Add(archsimd.LoadUint64x4((*[4]uint64)(vec[4:]))).
				Store((*[4]uint64)(dst[4:]))
		}
	default:
		addVector = func(dst, vec *[8]uint64) {
			dst[0] += vec[0]
			dst[1] += vec[1]
			dst[2] += vec[2]
			dst[3] += vec[3]
			dst[4] += vec[4]
			dst[5] += vec[5]
			dst[6] += vec[6]
			dst[7] += vec[7]
		}
	}
}

// Adds all the elements from src + offset
// vector is the already prepared vector of [8]uint64{offset}
func addOffsetFrom(dst *roaring64.Bitmap, src *roaring64.Bitmap, cached *[8]uint64, vector *[8]uint64) {
	for it := src.ManyIterator(); ; {
		n := it.NextMany(cached[:])

		addVector(cached, vector)

		dst.AddMany(cached[:n])
		if n < 8 {
			break
		}
	}
}
