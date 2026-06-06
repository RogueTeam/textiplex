package tuple

import (
	"unsafe"

	"github.com/zeebo/xxh3"
)

type Tuple2[T any] struct {
	A, B T
}

func (t *Tuple2[T]) Hash() (hash uint64) {
	return xxh3.Hash(unsafe.Slice((*byte)(unsafe.Pointer(t)), unsafe.Sizeof(Tuple2[T]{})))
}

type Tuple3[T any] struct {
	A, B, C T
}

func (t *Tuple3[T]) Hash() (hash uint64) {
	return xxh3.Hash(unsafe.Slice((*byte)(unsafe.Pointer(t)), unsafe.Sizeof(Tuple3[T]{})))
}
