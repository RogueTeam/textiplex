package storage

import "github.com/RoaringBitmap/roaring/roaring64"

// Adds all the elements from src + offset
func addOffsetFrom(dst *roaring64.Bitmap, src *roaring64.Bitmap, offset uint64, vector *[8]uint64) {
	for it := src.ManyIterator(); ; {
		n := it.NextMany(vector[:])

		vector[0] += offset
		vector[1] += offset
		vector[2] += offset
		vector[3] += offset
		vector[4] += offset
		vector[5] += offset
		vector[6] += offset
		vector[7] += offset

		dst.AddMany(vector[:n])
		if n < 8 {
			break
		}
	}
}
