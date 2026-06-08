package query

const MaxLevenshteinLength = 60

func Levenshtein(s1, s2 []byte, k int) bool {
	if len(s1) >= MaxLevenshteinLength || len(s2) >= MaxLevenshteinLength {
		return false
	}
	// state is a 2D array: active[i2][d] = true if state (i2,d) is active
	// max i2 = len(s2)+1, max d = k+1
	// use a flat [(len(s2)+1) * (k+1)]bool array
	size := (len(s2) + 1) * (k + 1)
	stride := k + 1

	current := make([]bool, size)
	next := make([]bool, size)

	current[k] = true // (i2=0, d=k) -- your initial state

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
