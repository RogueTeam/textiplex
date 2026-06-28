package query

import (
	"cmp"
	"slices"

	"github.com/RogueTeam/textiplex/storage"
)

type Searcher struct {
	Storage           *storage.Storage
	BM25Saturation    float64
	BM25LengthPenalty float64
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

// Once a filtering and scoring are done, next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (s *Searcher) ResolveScores(ctx *QueryContext) (idxs []uint32) {
	type scoreEntry struct {
		docIdx uint32
		score  float64
	}

	scores := make([]scoreEntry, 0, ctx.Bitmap.GetCardinality())

	var docIdxs [32]uint32
	it := ctx.Bitmap.ManyIterator()
	for {
		n := it.NextMany(docIdxs[:])
		for _, docIdx := range docIdxs[:n] {
			score := ctx.Scores[docIdx]
			if score == 0 {
				continue
			}

			scores = append(scores, scoreEntry{
				score:  score,
				docIdx: docIdx,
			})
		}

		if n < 32 {
			break
		}
	}

	slices.SortFunc(
		scores,
		func(a, b scoreEntry) int {
			scoreCmp := cmp.Compare(b.score, a.score)
			if scoreCmp == 0 {
				return cmp.Compare(b.docIdx, a.docIdx)
			}
			return scoreCmp
		},
	)

	idxs = make([]uint32, len(scores))
	for index := range scores {
		idxs[index] = scores[index].docIdx
	}
	return idxs
}
