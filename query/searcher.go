package query

import (
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
func (s *Searcher) ResolveScores(ctx *QueryContext, doCopy bool) (scores []Score) {
	if ctx.Scoring.Len() == 0 {
		return nil
	}
	// ToArray is a per-container bulk fill, far cheaper than draining a
	// ManyIterator in fixed-size batches, and the ascending order is the same.
	if doCopy {
		scores = slices.Clone(ctx.Scoring.Scores)
	} else {
		scores = ctx.Scoring.Scores
	}
	slices.SortFunc(
		scores,
		func(a, b Score) int {
			return cmp.Compare(b.Value, a.Value)
		},
	)
	return scores
}
