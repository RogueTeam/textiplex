package fastlog

import "unsafe"

// Based on https://quadst.rip/ln-approx
func Ln(x float32) (ro float32) {
	// unsigned int bx = * (unsigned int *) (&x);
	// unsigned int ex = bx >> 23;
	// signed int t = (signed int)ex-(signed int)127;
	// unsigned int s = (t < 0) ? (-t) : t;
	// bx = 1065353216 | (bx & 8388607);
	// x = * (float *) (&bx);
	// return -1.49278+(2.11263+(-0.729104+0.10969*x)*x)*x+0.6931471806*t;
	bx := *(*uint32)(unsafe.Pointer(&x))
	ex := bx >> 23
	t := float32(int(ex) - 127)
	bx = 1065353216 | (bx & 8388607)
	x1 := *(*float32)(unsafe.Pointer(&bx))
	x2 := x1 * x1
	x3 := x1 * x1 * x1
	return (2.11263*x1 + -0.729104*x2) + (0.10969*x3 + 0.6931471806*t) + -1.49278
}
