package numeric

import (
	"encoding/binary"
	"math"

	"golang.org/x/exp/constraints"
)

// Encodes a float64 so negative numbers can still be sorted before
func PutSortableFloat[T constraints.Float](dst []byte, v T) {
	bits := math.Float64bits(float64(v))
	if bits&(1<<63) != 0 {
		bits ^= 0xFFFFFFFFFFFFFFFF // negative: flip everything
	} else {
		bits |= 1 << 63 // non-negative: set the sign bit
	}
	binary.BigEndian.PutUint64(dst, bits)
}

func DecodeFloat(src []byte) float64 {
	bits := binary.BigEndian.Uint64(src)
	if bits&(1<<63) != 0 {
		bits &^= 1 << 63 // was non-negative: clear sign bit
	} else {
		bits ^= 0xFFFFFFFFFFFFFFFF // was negative
	}
	return math.Float64frombits(bits)
}
