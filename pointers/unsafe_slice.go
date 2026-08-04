package pointers

import "unsafe"

func UnsafeSliceBytes[T any](s []T) (b []byte) {
	return unsafe.Slice((*byte)(unsafe.Pointer(&s[0])), uintptr(len(s))*unsafe.Sizeof(s[0]))
}

func UnsafeValueBytes[T any](v *T) (s []byte) {
	return unsafe.Slice((*byte)(unsafe.Pointer(v)), unsafe.Sizeof(*v))
}
