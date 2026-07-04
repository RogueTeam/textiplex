package query

import (
	"cmp"
	"slices"
	"strings"

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

const ManyIteratorBatchSize = 8

// Once a filtering and scoring are done, next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (s *Searcher) ResolveScores(ctx *QueryContext) (idxs []uint32) {
	type scoreEntry struct {
		docIdx uint32
		score  float64
	}

	// ToArray is a per-container bulk fill, far cheaper than draining a
	// ManyIterator in fixed-size batches, and the ascending order is the same.
	candidates := ctx.Bitmap.ToArray()
	scores := make([]scoreEntry, 0, len(candidates))

	for _, docIdx := range candidates {
		score := ctx.Scores[docIdx]
		if score == 0 {
			continue
		}

		scores = append(scores, scoreEntry{
			score:  score,
			docIdx: docIdx,
		})
	}

	slices.SortFunc(
		scores,
		func(a, b scoreEntry) int {
			scoreCmp := cmp.Compare(b.score, a.score)
			if scoreCmp == 0 {
				return strings.Compare(s.Storage.DocumentsIds[b.docIdx].Value.UnsafeString(), s.Storage.DocumentsIds[a.docIdx].Value.UnsafeString())
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
