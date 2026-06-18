package tuple

import (
	"unsafe"

	"github.com/zeebo/xxh3"
)

func Hash2[T any](a, b T) (hash uint64) {
	var tuple = Tuple2[T]{A: a, B: b}
	return tuple.Hash()
}

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

// Same as hash but uses Tuple2[T] Size
func (t *Tuple3[T]) Hash2() (hash uint64) {
	return xxh3.Hash(unsafe.Slice((*byte)(unsafe.Pointer(t)), unsafe.Sizeof(Tuple2[T]{})))
}
