package query

import (
	"iter"
)

const MaxLevenshteinLength = 60

func LevenshteinSeeds(src []byte) (seq iter.Seq[[]byte]) {
	return func(yield func([]byte) bool) {
		// Yield as prefix
		for n := 1; n < len(src); n++ {
			sub := src[:len(src)-n]
			if !yield(sub) {
				return
			}
		}
		// Yield as suffix
		for n := 1; n < len(src); n++ {
			sub := src[n:]
			if !yield(sub) {
				return
			}
		}
	}
}

// Computes the LevenshteinMatch of between two byte arrays
// Max Supported K is 3
// Max supported string length is MaxLevenshteinLength
func LevenshteinMatch(s1, s2 []byte, k int) (match bool) {
	// Prevent exploits from attackers wanting a DDoS by forcing the server to allocate a huge buffer
	if len(s1) >= MaxLevenshteinLength || len(s2) >= MaxLevenshteinLength {
		return false
	}

	if k > 3 {
		return false
	}

	stride := k + 1

	size := (len(s2) + 1) * (k + 1)
	// index is computed from
	// i2 (character position on string 2) * stride + k
	buffer := make([]bool, 2*size)
	current := buffer[:size]
	next := buffer[size:]

	current[k] = true // (i2=0, d=k) -- your initial state

	// Once all characters of string 1
	// are computed against string 2
	// for each character position if any resolved to true, the strings matches
	for _, c := range s1 {
		clear(next)
		for i2 := 0; i2 <= len(s2); i2++ {
			for d := 0; d <= k; d++ {
				if !current[i2*stride+d] {
					continue
				}
				// deletion
				if d >= 1 {
					next[i2*stride+(d-1)] = true
					if i2+1 <= len(s2) {
						next[(i2+1)*stride+(d-1)] = true
					}
				}
				// match + insertions
				for dd := range min(d+1, len(s2)-i2) {
					if c == s2[i2+dd] {
						next[(i2+dd+1)*stride+(d-dd)] = true
					}
				}
			}
		}
		current, next = next, current
	}

	for i2 := 0; i2 <= len(s2); i2++ {
		for d := 0; d <= k; d++ {
			if current[i2*stride+d] && len(s2)-i2 <= d {
				return true
			}
		}
	}
	return false
}
