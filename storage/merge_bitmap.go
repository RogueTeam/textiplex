package storage

import (
	"simd/archsimd"
	"unsafe"

	"github.com/RoaringBitmap/roaring/roaring64"
)

var addVector func(dst, vec *[8]uint64)

func init() {
	switch {
	case archsimd.X86.AVX512():
		addVector = func(dst, vec *[8]uint64) {
			dstVec := archsimd.LoadUint64x8(dst)
			vecVec := archsimd.LoadUint64x8(vec)

			dstVec.Add(vecVec).Store(dst)
		}
	case archsimd.X86.AVX2():
		addVector = func(dst, vec *[8]uint64) {
			dst1Ptr := (*[4]uint64)(unsafe.Pointer(&dst[0]))
			dst2Ptr := (*[4]uint64)(unsafe.Pointer(&dst[4]))
			vec1Ptr := (*[4]uint64)(unsafe.Pointer(&vec[0]))
			vec2Ptr := (*[4]uint64)(unsafe.Pointer(&vec[4]))

			dstVec1 := archsimd.LoadUint64x4(dst1Ptr)
			vecVec1 := archsimd.LoadUint64x4(vec1Ptr)

			dstVec2 := archsimd.LoadUint64x4(dst2Ptr)
			vecVec2 := archsimd.LoadUint64x4(vec2Ptr)

			dstVec1.Add(vecVec1).Store((*[4]uint64)(unsafe.Pointer(&dst[0])))
			dstVec2.Add(vecVec2).Store((*[4]uint64)(unsafe.Pointer(&dst[4])))
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
