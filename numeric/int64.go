package numeric

import (
	"encoding/binary"

	"golang.org/x/exp/constraints"
)

// Encodes a int64 so negative numbers can still be sorted before
func PutSortableInteger[T constraints.Signed](dst []byte, v T) {
	binary.BigEndian.PutUint64(dst, uint64(v)^(1<<63))
}

func DecodeInteger(src []byte) int64 {
	return int64(binary.BigEndian.Uint64(src) ^ (1 << 63))
}
