package pointers

import "unsafe"

func UnsafeSliceBytes[T any](s []T) (b []byte) {
	var v T
	ptr := (*byte)(unsafe.Pointer(&s[0]))
	elemSize := unsafe.Sizeof(v)

	return unsafe.Slice(ptr, uintptr(len(s))*elemSize)
}

func UnsafeValueBytes[T any](v *T) (s []byte) {
	return unsafe.Slice((*byte)(unsafe.Pointer(v)), unsafe.Sizeof(*v))
}
