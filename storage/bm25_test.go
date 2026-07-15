package storage_test

import (
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/stretchr/testify/assert"
)

// ── BM25 scoring unit checks (no index) ───────────────────────────────────────

func TestBM25Primitives(t *testing.T) {
	assertions := assert.New(t)

	t.Run("idf decreases as term gets more common", func(t *testing.T) {
		rare := storage.InverseDocumentFrequency(1000, 1)
		common := storage.InverseDocumentFrequency(1000, 500)
		assertions.Greater(rare, common, "rarer term must have higher idf")
		assertions.Greater(common, float32(0.0))
	})

	t.Run("normalized tf saturates", func(t *testing.T) {
		// As tf→∞ this formula approaches (saturation+1): the saturation*lengthNorm
		// term in the denominator becomes negligible against tf.
		ceiling := float32(storage.DefaultSaturation + 1.0) // 2.2
		hi := storage.NormalizedTF(1_000_000, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.Less(hi, ceiling)
		assertions.InDelta(ceiling, hi, 0.01, "huge tf should sit just under the ceiling")
	})

	t.Run("normalized tf monotonic in tf", func(t *testing.T) {
		a := storage.NormalizedTF(1, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		b := storage.NormalizedTF(2, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.Greater(b, a)
	})

	t.Run("length penalty punishes long docs", func(t *testing.T) {
		short := storage.NormalizedTF(1, 5, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		long := storage.NormalizedTF(1, 20, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.Greater(short, long, "same tf, longer doc must score lower")
	})

	t.Run("scores are finite", func(t *testing.T) {
		score := storage.ScoreTermBM25(1000, 3, 4, 12, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.Greater(score, float32(0.0))
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// BM25 primitive math (no index)
// ══════════════════════════════════════════════════════════════════════════════

func TestBM25IDFExtended(t *testing.T) {
	t.Run("strictly decreasing as the term gets more common", func(t *testing.T) {
		assertions := assert.New(t)
		a := storage.InverseDocumentFrequency(1000, 1)
		b := storage.InverseDocumentFrequency(1000, 10)
		c := storage.InverseDocumentFrequency(1000, 100)
		d := storage.InverseDocumentFrequency(1000, 500)
		e := storage.InverseDocumentFrequency(1000, 900)
		assertions.Greater(a, b)
		assertions.Greater(b, c)
		assertions.Greater(c, d)
		assertions.Greater(d, e)
	})

	t.Run("always positive even when present in every doc", func(t *testing.T) {
		assertions := assert.New(t)
		all := storage.InverseDocumentFrequency(1000, 1000)
		assertions.Greater(all, float32(0.0), "smoothed idf must stay positive at n==N")
	})

	t.Run("rarer beats omnipresent", func(t *testing.T) {
		assertions := assert.New(t)
		rare := storage.InverseDocumentFrequency(1000, 1)
		all := storage.InverseDocumentFrequency(1000, 1000)
		assertions.Greater(rare, all)
	})
}

func TestBM25NormalizedTFExtended(t *testing.T) {
	t.Run("zero tf yields zero", func(t *testing.T) {
		assertions := assert.New(t)
		got := storage.NormalizedTF(0, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.Zero(got)
	})

	t.Run("strictly monotonic in tf", func(t *testing.T) {
		assertions := assert.New(t)
		a := storage.NormalizedTF(1, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		b := storage.NormalizedTF(2, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		c := storage.NormalizedTF(5, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.Greater(b, a)
		assertions.Greater(c, b)
	})

	t.Run("length penalty zero removes length dependence", func(t *testing.T) {
		assertions := assert.New(t)
		short := storage.NormalizedTF(3, 5, 100, storage.DefaultSaturation, float32(0.0))
		long := storage.NormalizedTF(3, 500, 100, storage.DefaultSaturation, float32(0.0))
		assertions.InDelta(short, long, 1e-12, "with b=0 doc length must not matter")
	})

	t.Run("invariant to penalty when docLen equals avgDocLen", func(t *testing.T) {
		assertions := assert.New(t)
		// factor (1 - b + b*docLen/avgDocLen) == 1 when docLen==avgDocLen, any b.
		noPenalty := storage.NormalizedTF(3, 10, 10, storage.DefaultSaturation, float32(0.0))
		fullPenalty := storage.NormalizedTF(3, 10, 10, storage.DefaultSaturation, 1.0)
		assertions.InDelta(noPenalty, fullPenalty, 1e-12)
	})

	t.Run("higher saturation raises the ceiling", func(t *testing.T) {
		assertions := assert.New(t)
		low := storage.NormalizedTF(1_000_000, 10, 10, 1.2, storage.DefaultLengthPenalty)
		high := storage.NormalizedTF(1_000_000, 10, 10, 2.0, storage.DefaultLengthPenalty)
		assertions.Greater(high, low, "larger k1 must lift the saturation ceiling")
	})
}

func TestBM25ScoreExtended(t *testing.T) {
	t.Run("score equals idf times normalized tf", func(t *testing.T) {
		assertions := assert.New(t)
		idf := storage.InverseDocumentFrequency(1000, 7)
		ntf := storage.NormalizedTF(3, 12, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		got := storage.ScoreTermBM25(1000, 7, 3, 12, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.InDelta(idf*ntf, got, 1e-9, "ScoreTermBM25 must compose idf and normalized tf")
	})

	t.Run("monotonic in tf", func(t *testing.T) {
		assertions := assert.New(t)
		a := storage.ScoreTermBM25(1000, 7, 1, 12, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		b := storage.ScoreTermBM25(1000, 7, 2, 12, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		c := storage.ScoreTermBM25(1000, 7, 5, 12, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.Greater(b, a)
		assertions.Greater(c, b)
	})

	t.Run("rarer term scores higher at equal tf and length", func(t *testing.T) {
		assertions := assert.New(t)
		rare := storage.ScoreTermBM25(1000, 1, 3, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		common := storage.ScoreTermBM25(1000, 500, 3, 10, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty)
		assertions.Greater(rare, common)
	})

	t.Run("finite and positive across a parameter spread", func(t *testing.T) {
		assertions := assert.New(t)
		scores := []float32{
			storage.ScoreTermBM25(10, 1, 1, 1, 1, storage.DefaultSaturation, storage.DefaultLengthPenalty),
			storage.ScoreTermBM25(1_000_000, 1, 50, 3, 200, storage.DefaultSaturation, storage.DefaultLengthPenalty),
			storage.ScoreTermBM25(1_000_000, 999_999, 1, 1000, 10, storage.DefaultSaturation, storage.DefaultLengthPenalty),
		}
		for i, sc := range scores {
			assertions.Greater(sc, float32(0.0), "score %d must be positive", i)
		}
	})
}

// ══════════════════════════════════════════════════════════════════════════════
// Deep BM25 primitive math (no index)
// ══════════════════════════════════════════════════════════════════════════════

func TestBM25PrimitivesDeep(t *testing.T) {
	t.Run("idf at zero doc frequency exceeds idf at one", func(t *testing.T) {
		assertions := assert.New(t)
		none := storage.InverseDocumentFrequency(1000, 0)
		one := storage.InverseDocumentFrequency(1000, 1)
		assertions.Greater(none, one)
		assertions.Greater(one, float32(0.0))
	})

	t.Run("idf strictly decreasing across a sampled sweep", func(t *testing.T) {
		assertions := assert.New(t)
		prev := storage.InverseDocumentFrequency(10000, 1)
		for _, n := range []uint64{2, 5, 50, 500, 5000, 9999} {
			cur := storage.InverseDocumentFrequency(10000, n)
			assertions.Greater(prev, cur, "idf must drop as n grows to %d", n)
			prev = cur
		}
	})

	t.Run("normalized tf with zero saturation equals one", func(t *testing.T) {
		assertions := assert.New(t)
		// With k1==0 the denominator collapses to tf, so the ratio is 1 for any tf>0.
		got := storage.NormalizedTF(7, 123, 10, float32(0.0), storage.DefaultLengthPenalty)
		assertions.InDelta(1.0, got, 1e-12)
	})

	t.Run("normalized tf approaches k1 plus one ceiling", func(t *testing.T) {
		assertions := assert.New(t)
		const k1 = float32(2.0)
		got := storage.NormalizedTF(1_000_000, 10, 10, k1, storage.DefaultLengthPenalty)
		assertions.Less(got, k1+1.0)
		assertions.InDelta(k1+1.0, got, 0.01)
	})

	t.Run("normalized tf strictly decreasing in doc length at full penalty", func(t *testing.T) {
		assertions := assert.New(t)
		short := storage.NormalizedTF(3, 5, 10, storage.DefaultSaturation, 1.0)
		mid := storage.NormalizedTF(3, 10, 10, storage.DefaultSaturation, 1.0)
		long := storage.NormalizedTF(3, 40, 10, storage.DefaultSaturation, 1.0)
		assertions.Greater(short, mid)
		assertions.Greater(mid, long)
	})

	t.Run("score composes idf and normalized tf with custom params", func(t *testing.T) {
		assertions := assert.New(t)
		const k1, b = 2.0, 0.5
		idf := storage.InverseDocumentFrequency(5000, 13)
		ntf := storage.NormalizedTF(4, 20, 15, k1, b)
		got := storage.ScoreTermBM25(5000, 13, 4, 20, 15, k1, b)
		assertions.InDelta(idf*ntf, got, 1e-9)
	})

	t.Run("score length penalty zero ignores doc length", func(t *testing.T) {
		assertions := assert.New(t)
		short := storage.ScoreTermBM25(1000, 7, 3, 5, 100, storage.DefaultSaturation, float32(0.0))
		long := storage.ScoreTermBM25(1000, 7, 3, 500, 100, storage.DefaultSaturation, float32(0.0))
		assertions.InDelta(short, long, 1e-12)
	})

	t.Run("score finite and positive across extreme params", func(t *testing.T) {
		assertions := assert.New(t)
		scores := []float32{
			storage.ScoreTermBM25(1, 1, 1, 1, 1, storage.DefaultSaturation, storage.DefaultLengthPenalty),
			storage.ScoreTermBM25(10_000_000, 1, 1, 1, 1, storage.DefaultSaturation, storage.DefaultLengthPenalty),
			storage.ScoreTermBM25(10_000_000, 9_999_999, 5000, 1, 100000, storage.DefaultSaturation, storage.DefaultLengthPenalty),
		}
		for i, sc := range scores {
			assertions.Greater(sc, float32(0.0), "score %d must stay positive", i)
		}
	})
}
