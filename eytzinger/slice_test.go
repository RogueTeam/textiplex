package eytzinger_test

import (
	"math/rand"
	"slices"
	"testing"

	"github.com/RogueTeam/textiplex/eytzinger"
	"github.com/stretchr/testify/assert"
)

// ── EasyBuild ─────────────────────────────────────────────────────────────────

func TestEasyBuild(t *testing.T) {
	type Test struct {
		name    string
		src     []uint32
		wantLen int // expected len(dst) == len(src)+1
	}

	tests := []Test{
		{
			name:    "empty slice",
			src:     []uint32{},
			wantLen: 1,
		},
		{
			name:    "single element",
			src:     []uint32{42},
			wantLen: 2,
		},
		{
			name:    "two elements",
			src:     []uint32{1, 2},
			wantLen: 3,
		},
		{
			name:    "power-of-two size",
			src:     []uint32{1, 2, 3, 4, 5, 6, 7, 8},
			wantLen: 9,
		},
		{
			name:    "non-power-of-two size",
			src:     []uint32{10, 20, 30, 40, 50},
			wantLen: 6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			dst := eytzinger.EasyBuild(tc.src)
			assertions.Len(dst, tc.wantLen, "output length must be len(src)+1")
			assertions.Zero(dst[0], "index 0 must be the unused sentinel slot")
		})
	}
}

// EasyBuild must contain exactly the same elements as the source (index 0 aside).
func TestEasyBuildPreservesElements(t *testing.T) {
	assertions := assert.New(t)

	src := []uint32{3, 7, 12, 19, 25, 31, 44, 55}
	dst := eytzinger.EasyBuild(src)

	got := slices.Clone(dst[1:]) // drop the unused slot
	slices.Sort(got)

	want := slices.Clone(src)
	slices.Sort(want)

	assertions.Equal(want, got, "EasyBuild must contain the same multiset of elements as the source")
}

// ── SearchInteger ─────────────────────────────────────────────────────────────

func TestSearchIntegerFound(t *testing.T) {
	type Test struct {
		name   string
		src    []uint32
		target uint32
	}

	tests := []Test{
		{
			name:   "single element found",
			src:    []uint32{42},
			target: 42,
		},
		{
			name:   "first element",
			src:    []uint32{1, 2, 3, 4, 5},
			target: 1,
		},
		{
			name:   "last element",
			src:    []uint32{1, 2, 3, 4, 5},
			target: 5,
		},
		{
			name:   "middle element",
			src:    []uint32{10, 20, 30, 40, 50},
			target: 30,
		},
		{
			name:   "power-of-two size, first",
			src:    []uint32{1, 2, 3, 4, 5, 6, 7, 8},
			target: 1,
		},
		{
			name:   "power-of-two size, last",
			src:    []uint32{1, 2, 3, 4, 5, 6, 7, 8},
			target: 8,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			dst := eytzinger.EasyBuild(tc.src)
			i := eytzinger.SearchInteger(dst, tc.target)
			assertions.NotEqual(-1, i, "target %d must be found", tc.target)
			assertions.Equal(tc.target, dst[i], "returned index must point to the target value")
		})
	}
}

func TestSearchIntegerNotFound(t *testing.T) {
	type Test struct {
		name   string
		src    []uint32
		target uint32
	}

	tests := []Test{
		{
			name:   "empty slice",
			src:    []uint32{},
			target: 1,
		},
		{
			name:   "below minimum",
			src:    []uint32{10, 20, 30},
			target: 5,
		},
		{
			name:   "above maximum",
			src:    []uint32{10, 20, 30},
			target: 99,
		},
		{
			name:   "gap between elements",
			src:    []uint32{10, 20, 30},
			target: 15,
		},
		{
			name:   "single element miss",
			src:    []uint32{42},
			target: 43,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			dst := eytzinger.EasyBuild(tc.src)
			i := eytzinger.SearchInteger(dst, tc.target)
			assertions.Equal(-1, i, "absent target %d must return -1", tc.target)
		})
	}
}

// Every element in a sorted slice must be findable after EasyBuild.
func TestSearchIntegerAllElements(t *testing.T) {
	assertions := assert.New(t)

	src := []uint32{2, 5, 11, 17, 23, 29, 37, 41, 53, 61, 67, 71}
	dst := eytzinger.EasyBuild(src)

	for _, v := range src {
		i := eytzinger.SearchInteger(dst, v)
		assertions.NotEqual(-1, i, "element %d must be found", v)
		assertions.Equal(v, dst[i], "returned index must point to %d", v)
	}
}

// ── Search (generic comparator) ───────────────────────────────────────────────

func TestSearchFound(t *testing.T) {
	assertions := assert.New(t)

	cmp := func(a, b uint32) int { return int(a) - int(b) }
	src := []uint32{3, 7, 12, 19, 25}
	dst := eytzinger.EasyBuild(src)

	for _, v := range src {
		i := eytzinger.Search(dst, v, cmp)
		assertions.NotEqual(-1, i, "element %d must be found via Search", v)
		assertions.Equal(v, dst[i])
	}
}

func TestSearchNotFound(t *testing.T) {
	assertions := assert.New(t)

	cmp := func(a, b uint32) int { return int(a) - int(b) }
	src := []uint32{3, 7, 12, 19, 25}
	dst := eytzinger.EasyBuild(src)

	for _, missing := range []uint32{0, 1, 8, 13, 99} {
		i := eytzinger.Search(dst, missing, cmp)
		assertions.Equal(-1, i, "absent value %d must return -1", missing)
	}
}

// Search with a subtraction comparator must agree with SearchInteger on all inputs.
func TestSearchAgreesWithSearchInteger(t *testing.T) {
	assertions := assert.New(t)

	src := []uint32{4, 9, 16, 25, 36, 49, 64, 81}
	dst := eytzinger.EasyBuild(src)
	cmp := func(a, b uint32) int { return int(a) - int(b) }

	for _, v := range src {
		iGeneric := eytzinger.Search(dst, v, cmp)
		iInteger := eytzinger.SearchInteger(dst, v)
		assertions.Equal(iInteger, iGeneric, "Search and SearchInteger must agree for value %d", v)
	}
}

// ── Build (low-level) ─────────────────────────────────────────────────────────

func TestBuildLowLevel(t *testing.T) {
	assertions := assert.New(t)

	src := []uint32{1, 2, 3, 4, 5, 6, 7}
	dst := make([]uint32, len(src)+1)
	eytzinger.Build(dst, src, 0, 1)

	// Index 0 must be untouched (zero value).
	assertions.Zero(dst[0])

	// Every src element must appear somewhere in dst[1:].
	got := slices.Clone(dst[1:])
	slices.Sort(got)
	want := slices.Clone(src)
	slices.Sort(want)
	assertions.Equal(want, got)
}

// ── Invariants ────────────────────────────────────────────────────────────────

// The heap property: for every internal node k, dst[k] < dst[2k] and dst[k] > dst[2k+1]
// (left child is smaller, right is larger), mirroring a BST in BFS order.
func TestEytzingerBSTProperty(t *testing.T) {
	assertions := assert.New(t)

	src := []uint32{1, 3, 5, 7, 9, 11, 13, 15}
	dst := eytzinger.EasyBuild(src)
	n := len(dst)

	for k := 1; k < n; k++ {
		left := 2 * k
		right := 2*k + 1
		if left < n {
			assertions.Less(dst[left], dst[k],
				"left child dst[%d]=%d must be less than parent dst[%d]=%d", left, dst[left], k, dst[k])
		}
		if right < n {
			assertions.Greater(dst[right], dst[k],
				"right child dst[%d]=%d must be greater than parent dst[%d]=%d", right, dst[right], k, dst[k])
		}
	}
}

// For any sorted input, every element is findable and no spurious hit is returned.
func TestSearchIntegerCorrectOnRandomInputs(t *testing.T) {
	assertions := assert.New(t)
	rng := rand.New(rand.NewSource(42))

	const n = 128
	set := make(map[uint32]bool, n)
	for len(set) < n {
		set[uint32(rng.Intn(1000))] = true
	}
	src := make([]uint32, 0, n)
	for v := range set {
		src = append(src, v)
	}
	slices.Sort(src)

	dst := eytzinger.EasyBuild(src)

	// Every element in src must be found.
	for _, v := range src {
		i := eytzinger.SearchInteger(dst, v)
		assertions.NotEqual(-1, i, "element %d must be found", v)
		assertions.Equal(v, dst[i])
	}

	// Values not in src must not be found.
	for candidate := uint32(0); candidate < 1000; candidate++ {
		if set[candidate] {
			continue
		}
		i := eytzinger.SearchInteger(dst, candidate)
		assertions.Equal(-1, i, "absent value %d must return -1", candidate)
	}
}

// SearchInteger results must match a naive linear scan on the same input.
func TestSearchIntegerMatchesLinearScan(t *testing.T) {
	assertions := assert.New(t)
	rng := rand.New(rand.NewSource(99))

	const n = 64
	src := make([]uint32, n)
	for i := range src {
		src[i] = uint32(i * 3) // 0,3,6,...,189 — no duplicates
	}

	dst := eytzinger.EasyBuild(src)

	linearFind := func(s []uint32, target uint32) bool {
		for _, v := range s {
			if v == target {
				return true
			}
		}
		return false
	}

	for range 200 {
		target := uint32(rng.Intn(200))
		wantFound := linearFind(src, target)
		i := eytzinger.SearchInteger(dst, target)
		gotFound := i != -1
		assertions.Equal(wantFound, gotFound, "SearchInteger and linear scan must agree for target %d", target)
	}
}

// ── Property: non-power-of-two sizes ─────────────────────────────────────────

func TestNonPowerOfTwoSizes(t *testing.T) {
	assertions := assert.New(t)

	for size := 1; size <= 33; size++ {
		src := make([]uint32, size)
		for i := range src {
			src[i] = uint32(i*2 + 1) // odd numbers, no duplicates
		}
		dst := eytzinger.EasyBuild(src)

		for _, v := range src {
			i := eytzinger.SearchInteger(dst, v)
			assertions.NotEqual(-1, i, "size=%d: element %d must be found", size, v)
			assertions.Equal(v, dst[i], "size=%d: returned index must point to %d", size, v)
		}
	}
}

// ── Property / fuzz-style invariants ─────────────────────────────────────────

// Across random sorted slices, EasyBuild+SearchInteger must behave like a map lookup.
func TestPropertySearchEquivalentToMap(t *testing.T) {
	assertions := assert.New(t)

	for seed := int64(0); seed < 10; seed++ {
		rng := rand.New(rand.NewSource(seed))
		n := 10 + rng.Intn(200)

		set := make(map[uint32]bool, n)
		for len(set) < n {
			set[uint32(rng.Intn(10000))] = true
		}
		src := make([]uint32, 0, n)
		for v := range set {
			src = append(src, v)
		}
		slices.Sort(src)

		dst := eytzinger.EasyBuild(src)

		for probe := uint32(0); probe < 10000; probe += uint32(rng.Intn(5) + 1) {
			wantFound := set[probe]
			gotIdx := eytzinger.SearchInteger(dst, probe)
			gotFound := gotIdx != -1
			assertions.Equal(wantFound, gotFound,
				"seed=%d probe=%d: SearchInteger must agree with map lookup", seed, probe)
		}
	}
}

// Adding more elements never causes previously-findable elements to disappear.
func TestPropertyGrowingSlicePreservesElements(t *testing.T) {
	assertions := assert.New(t)

	src := []uint32{}
	for _, v := range []uint32{5, 10, 15, 20, 25, 30, 35, 40, 45, 50} {
		src = append(src, v)
		dst := eytzinger.EasyBuild(src)
		for _, want := range src {
			i := eytzinger.SearchInteger(dst, want)
			assertions.NotEqual(-1, i, "after inserting %d, element %d must still be found", v, want)
		}
	}
}

// ── Large input smoke test ────────────────────────────────────────────────────

func TestLargeInput(t *testing.T) {
	assertions := assert.New(t)

	const n = 1 << 16 // 65536
	src := make([]uint32, n)
	for i := range src {
		src[i] = uint32(i * 2) // even numbers 0..131070
	}

	dst := eytzinger.EasyBuild(src)
	assertions.Len(dst, n+1)

	// Sample a spread of elements.
	for _, v := range []uint32{0, 2, 100, 1000, 65534, 131070} {
		i := eytzinger.SearchInteger(dst, v)
		assertions.NotEqual(-1, i, "element %d must be found in large input", v)
		assertions.Equal(v, dst[i])
	}

	// Odd numbers must not be found.
	for _, v := range []uint32{1, 3, 101, 65535} {
		i := eytzinger.SearchInteger(dst, v)
		assertions.Equal(-1, i, "odd value %d must not be found", v)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// sortedRange returns [start, start+n) as a []uint32.
func sortedRange(start, n int) []uint32 {
	s := make([]uint32, n)
	for i := range s {
		s[i] = uint32(start + i)
	}
	return s
}

// TestSearchIntegerContiguousRange checks every value in a contiguous range.
func TestSearchIntegerContiguousRange(t *testing.T) {
	assertions := assert.New(t)

	src := sortedRange(100, 50) // [100, 101, ..., 149]
	dst := eytzinger.EasyBuild(src)

	for v := uint32(100); v < 150; v++ {
		i := eytzinger.SearchInteger(dst, v)
		assertions.NotEqual(-1, i, "value %d in range must be found", v)
		assertions.Equal(v, dst[i])
	}

	for _, outside := range []uint32{0, 99, 150, 200} {
		i := eytzinger.SearchInteger(dst, outside)
		assertions.Equal(-1, i, "value %d outside range must not be found", outside)
	}
}

// ── Search with struct comparator ─────────────────────────────────────────────

type entry struct {
	Key   uint32
	Value string
}

func TestSearchStructComparator(t *testing.T) {
	type Test struct {
		name      string
		src       []entry
		targetKey uint32
		wantFound bool
	}

	tests := []Test{
		{
			name: "found in middle",
			src: []entry{
				{1, "one"}, {3, "three"}, {5, "five"}, {7, "seven"}, {9, "nine"},
			},
			targetKey: 5,
			wantFound: true,
		},
		{
			name: "found at start",
			src: []entry{
				{1, "one"}, {3, "three"}, {5, "five"},
			},
			targetKey: 1,
			wantFound: true,
		},
		{
			name: "not found between keys",
			src: []entry{
				{1, "one"}, {3, "three"}, {5, "five"},
			},
			targetKey: 2,
			wantFound: false,
		},
		{
			name: "not found beyond range",
			src: []entry{
				{1, "one"}, {3, "three"},
			},
			targetKey: 99,
			wantFound: false,
		},
	}

	cmp := func(a entry, b uint32) int { return int(a.Key) - int(b) }

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			dst := eytzinger.EasyBuild(tc.src)
			i := eytzinger.Search(dst, tc.targetKey, cmp)
			if tc.wantFound {
				assertions.NotEqual(-1, i, "key %d must be found", tc.targetKey)
				assertions.Equal(tc.targetKey, dst[i].Key)
			} else {
				assertions.Equal(-1, i, "key %d must not be found", tc.targetKey)
			}
		})
	}
}

// ── Determinism ───────────────────────────────────────────────────────────────

// EasyBuild on the same input must produce the same layout every call.
func TestEasyBuildDeterministic(t *testing.T) {
	assertions := assert.New(t)

	src := []uint32{5, 10, 15, 20, 25, 30, 35}
	a := eytzinger.EasyBuild(src)
	b := eytzinger.EasyBuild(src)
	assertions.Equal(a, b, "EasyBuild must be deterministic")
}

// SearchInteger on the same layout with the same target must return the same index.
func TestSearchIntegerDeterministic(t *testing.T) {
	assertions := assert.New(t)

	src := []uint32{2, 4, 6, 8, 10, 12, 14, 16}
	dst := eytzinger.EasyBuild(src)

	for range 5 {
		i1 := eytzinger.SearchInteger(dst, 8)
		i2 := eytzinger.SearchInteger(dst, 8)
		assertions.Equal(i1, i2, "SearchInteger must return the same index on repeated calls")
	}
}

// ── fmt dependency guard ──────────────────────────────────────────────────────

// Calling EasyBuild must not produce any output (guards against leftover fmt.Println).
// This test is intentionally placed last and relies on the test binary not printing
// to stdout during the build phase — it is a documentation reminder rather than a
// runtime assertion.
func TestEasyBuildNoDebugOutput(t *testing.T) {
	// If EasyBuild still contains fmt.Println this will surface in `go test -v`
	// output as unexpected lines. We just exercise the function here.
	_ = eytzinger.EasyBuild([]uint32{1, 2, 3})
}
