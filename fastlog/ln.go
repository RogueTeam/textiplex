package fastlog

import "unsafe"

// Based on https://quadst.rip/ln-approx
func Ln(x float32) float32 {
	bx := *(*uint32)(unsafe.Pointer(&x))
	ex := bx >> 23
	t := float32(int32(ex) - 127)
	bx = 1065353216 | (bx & 8388607)
	x = *(*float32)(unsafe.Pointer(&bx))
	return -1.49278 + (2.11263+(-0.729104+0.10969*x)*x)*x + 0.6931471806*t
}
