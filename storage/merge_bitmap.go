package storage

import "github.com/RoaringBitmap/roaring/roaring64"

// Adds all the elements from src + offset
func addOffsetFrom(dst *roaring64.Bitmap, src *roaring64.Bitmap, offset uint64) {
	const bitmapValuesLength = 8
	var bitmapValues [bitmapValuesLength]uint64

	for it := src.ManyIterator(); ; {
		n := it.NextMany(bitmapValues[:])

		bitmapValues[0] += offset
		bitmapValues[1] += offset
		bitmapValues[2] += offset
		bitmapValues[3] += offset
		bitmapValues[4] += offset
		bitmapValues[5] += offset
		bitmapValues[6] += offset
		bitmapValues[7] += offset

		dst.AddMany(bitmapValues[:n])
		if n < bitmapValuesLength {
			break
		}
	}
}
