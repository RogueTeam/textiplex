package eytzinger

import (
	"math/bits"

	"golang.org/x/exp/constraints"
)

func Build[T any](dst, src []T, i, k int) (next int) {
	if k < len(dst) {
		i = Build(dst, src, i, 2*k)
		dst[k] = src[i]
		i++
		i = Build(dst, src, i, 2*k+1)
	}
	return i
}

func EasyBuild[T any](s []T) []T {
	o := make([]T, len(s)+1) // index 0 unused
	Build(o, s, 0, 1)
	return o
}

func Search[T, K any](s []T, target K, cmp func(a T, b K) int) int {
	k := 1
	for k < len(s) {
		if cmp(s[k], target) < 0 {
			k = k*2 + 1
		} else {
			k = k * 2
		}
	}
	k >>= bits.TrailingZeros(^uint(k))
	if k < len(s) && cmp(s[k], target) == 0 {
		return k
	}
	return -1
}

func SearchInteger[T constraints.Integer](s []T, target T) int {
	k := 1
	for k < len(s) {
		if s[k] < target {
			k = k*2 + 1
		} else {
			k = k * 2
		}
	}
	k >>= bits.TrailingZeros(^uint(k))
	if k < len(s) && s[k] == target {
		return k
	}
	return -1
}
