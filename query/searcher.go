package query

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/RogueTeam/textiplex/storage"
)

type Searcher struct {
	Storage           *storage.Storage
	BM25Saturation    float32
	BM25LengthPenalty float32
	// Maximum amount of entries challenged against levenshtein fuzz algorithm
	LevenshteinM    int
	LevenshteinMaxK int
}

func New(s *storage.Storage) (searcher *Searcher) {
	searcher = &Searcher{
		Storage: s,
	}
	return searcher
}

const ManyIteratorBatchSize = 8

// Once a filtering and scoring are done, next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (s *Searcher) ResolveScores(ctx *QueryContext) (idxs []uint32) {
	if len(ctx.Scores) == 0 {
		return nil
	}
	// ToArray is a per-container bulk fill, far cheaper than draining a
	// ManyIterator in fixed-size batches, and the ascending order is the same.
	candidates := ctx.Bitmap.ToArray()
	slices.SortFunc(
		candidates,
		func(a, b uint32) int {
			scoreCmp := cmp.Compare(ctx.Scores[b], ctx.Scores[a])
			if scoreCmp == 0 {
				return bytes.Compare(s.Storage.DocumentsIds[b].Value.Bytes(), s.Storage.DocumentsIds[a].Value.Bytes())
			}
			return scoreCmp
		},
	)

	zeroIdx, found := slices.BinarySearchFunc(
		candidates, 0.0,
		func(e uint32, t float32) int {
			return cmp.Compare(t, ctx.Scores[e])
		},
	)
	if !found {
		return candidates
	}
	return candidates[:zeroIdx]
}
