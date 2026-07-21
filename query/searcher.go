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
func (s *Searcher) ResolveScores(ctx *QueryContext) (scores []Score) {
	if ctx.Scoring.Len() == 0 {
		return nil
	}
	// ToArray is a per-container bulk fill, far cheaper than draining a
	// ManyIterator in fixed-size batches, and the ascending order is the same.
	scores = slices.Clone(ctx.Scoring.Scores)
	slices.SortFunc(
		scores,
		func(a, b Score) int {
			scoreCmp := cmp.Compare(b.Value, a.Value)
			if scoreCmp == 0 {
				return bytes.Compare(s.Storage.DocumentsIds[b.Index].Value.Bytes(), s.Storage.DocumentsIds[a.Index].Value.Bytes())
			}
			return scoreCmp
		},
	)

	zeroIdx, found := slices.BinarySearchFunc(
		scores, 0.0,
		func(e Score, t float32) int {
			return cmp.Compare(t, e.Value)
		},
	)
	if !found {
		return scores
	}
	return scores[:zeroIdx]
}
