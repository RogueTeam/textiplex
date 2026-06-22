package storage

import (
	"github.com/RoaringBitmap/roaring"
)

const OffsetBitmapCachedSize = 16

// Adds all the elements from src + offset
// vector is the already prepared vector of [8]uint64{offset}
func addOffsetFrom(dst *roaring.Bitmap, src *roaring.Bitmap, cached *[OffsetBitmapCachedSize]uint32, offset uint32) {
	for it := src.ManyIterator(); ; {
		n := it.NextMany(cached[:])

		for i := range cached[:n] {
			cached[i] += offset
		}

		dst.AddMany(cached[:n])
		if n < 8 {
			break
		}
	}
}
