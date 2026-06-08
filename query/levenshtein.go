package query

import "iter"

const (
	MaxLevenshteinLength = 60
	DefaultLevenshteinK  = 1
	DefaultLevenshteinM  = 10
)

// LevenshteinSeeds yields prefix and suffix substrings of src to use as btree
// seek targets when doing heuristic fuzzy fallback. Not used by the automata
// traversal but kept for reference and potential hybrid use.
func LevenshteinSeeds(src []byte) (seq iter.Seq[[]byte]) {
	return func(yield func([]byte) bool) {
		// Shrink from right: "acount" -> "acoun", "acou", ...
		for n := 1; n < len(src); n++ {
			if !yield(src[:len(src)-n]) {
				return
			}
		}
		// Shrink from left: "acount" -> "count", "ount", ...
		// Covers leading-deletion candidates that sort before the query.
		for n := 1; n < len(src); n++ {
			if !yield(src[n:]) {
				return
			}
		}
	}
}

// LevenshteinMatch reports whether the edit distance between s1 and s2 is <= k.
// Max supported k is 3. Strings longer than MaxLevenshteinLength return false
// to prevent DoS via large allocation.
func LevenshteinMatch(s1, s2 []byte, k int) bool {
	if len(s1) >= MaxLevenshteinLength || len(s2) >= MaxLevenshteinLength {
		return false
	}

	if k > 3 {
		return false
	}

	stride := k + 1

	size := (len(s2) + 1) * stride
	buffer := make([]bool, 2*size)
	// index is computed from
	// i2 (character position on string 2) * stride + k
	current := buffer[:size]
	next := buffer[size:]

	// Initial state: (i2=0, d=k)
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
