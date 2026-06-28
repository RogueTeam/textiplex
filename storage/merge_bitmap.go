package storage

import (
	"github.com/RoaringBitmap/roaring"
)

const OffsetBitmapCachedSize = 16

// Adds all the elements from src + offset
// vector is the already prepared vector of [8]uint64{offset}
func addOffsetFrom(ctx *MergeContext, dst *roaring.Bitmap, src *roaring.Bitmap) {
	for it := src.ManyIterator(); ; {
		n := it.NextMany(ctx.CachedBitmapChunk[:])

		for i := range ctx.CachedBitmapChunk[:n] {
			ctx.CachedBitmapChunk[i] += ctx.DocumentOffset
		}

		dst.AddMany(ctx.CachedBitmapChunk[:n])
		if n < 8 {
			break
		}
	}
}
