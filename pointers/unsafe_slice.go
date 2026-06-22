package pointers

import "unsafe"

func UnsafeSlice[T any](v *T) (s []byte) {
	return unsafe.Slice((*byte)(unsafe.Pointer(v)), unsafe.Sizeof(*v))
}
