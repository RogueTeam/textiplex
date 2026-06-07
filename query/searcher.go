package query

import (
	"cmp"
	"slices"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tuple"
	"github.com/zeebo/xxh3"
)

type Searcher struct {
	Storage *storage.Storage
	// Helpers, not intended to be saved in any matter but could be used to improve query performance
	// FieldDocLengths maps fieldHash -> document index -> length
	// Use Tuple2.Hash for generating the keys
	// A: Field Hash
	// B: Document Index
	FieldDocLengths map[uint64]uint64
	// FieldTokenDocFrequencies field hash -> token hash -> document index -> frequency
	// Use Tuple3.Hash for generating the keys
	// A: Field Hash
	// B: Token Hash
	// C: Document Index
	FieldTokenDocFrequencies map[uint64]uint64
}

// Construct helper types to improve query performance
func (s *Searcher) BuildHelpers() {
	s.FieldDocLengths = make(map[uint64]uint64, len(s.Storage.Fields))
	s.FieldTokenDocFrequencies = make(map[uint64]uint64, len(s.Storage.Fields))

	var fieldsTokensDocsKey tuple.Tuple3[uint64]
	var fieldsDocsKey tuple.Tuple2[uint64]
	for fieldHash, field := range s.Storage.Fields {
		fieldsTokensDocsKey.A = fieldHash
		fieldsDocsKey.A = fieldHash
		for i := range field.DocumentLengths {
			docLength := &field.DocumentLengths[i]
			fieldsDocsKey.B = docLength.Index
			s.FieldDocLengths[fieldsDocsKey.Hash()] = docLength.Length
		}

		it := field.Tokens.Iter()
		for valid := it.First(); valid; valid = it.Next() {
			token := it.Item()
			fieldsTokensDocsKey.B = xxh3.Hash(token.Value)

			freqs := s.Storage.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

			for i := range freqs {
				fieldsTokensDocsKey.C = freqs[i].DocumentIndex
				s.FieldTokenDocFrequencies[fieldsTokensDocsKey.Hash()] = freqs[i].Frequency
			}
		}
		it.Release()
	}

}

func New(s *storage.Storage) (searcher *Searcher) {
	searcher = &Searcher{
		Storage: s,
	}
	searcher.BuildHelpers()
	return searcher
}

func (s *Searcher) UpdateScores(ctx *QueryContext, state *ClauseState) {
	token := state.Token
	field := state.Field

	tokenHash := xxh3.Hash(token.Value)

	fieldsTokenDocsKey := tuple.Tuple3[uint64]{A: state.FieldHash, B: tokenHash}
	fieldDocsKey := tuple.Tuple2[uint64]{A: state.FieldHash}

	for it := ctx.Bitmap.Iterator(); it.HasNext(); {
		docIdx := it.Next()

		fieldsTokenDocsKey.C = docIdx
		freq, found := s.FieldTokenDocFrequencies[fieldsTokenDocsKey.Hash()]
		if !found {
			continue
		}

		fieldDocsKey.B = docIdx
		docLength, found := s.FieldDocLengths[fieldDocsKey.Hash()]
		if !found {
			continue
		}

		scoreDelta := ScoreTermBM25(
			/* docCoun */ uint64(len(field.DocumentLengths)),
			/* tokenDocFreq */ token.FrequencyCount,
			/* tokenFreq */ freq,
			/* documentLength */ docLength,
			/* avgDocLength */ field.AvgDocumentLength,
			/* saturation */ DefaultSaturation,
			/* lengthPenalty */ DefaultLengthPenalty,
		)

		if state.Keyword != nil {
			ctx.Scores[docIdx] += state.Keyword.Boost * scoreDelta
		} else if state.Range != nil {
			ctx.Scores[docIdx] += state.Range.Boost * scoreDelta
		} else {
			// Should never match but is good guard to unknown cases
			ctx.Scores[docIdx] += scoreDelta
		}
	}
}

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (s *Searcher) FilterDocuments(ctx *QueryContext, q *SimpleQuery) {
	mustsCount := q.Musts.Count()
	shouldsCount := q.Shoulds.Count()
	mustNotsCount := q.MustNots.Count()

	if mustsCount > 0 {
		// Musts define the candidate set: intersection of all Must posting lists.
		var firstMust bool
		q.Musts.Iter(s.Storage, func(state *ClauseState) {
			pl := &s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap
			if !firstMust {
				ctx.Bitmap.Or(pl)
				firstMust = true
			} else {
				ctx.Bitmap.And(pl)
			}
		})
	} else if shouldsCount > 0 {
		// No Musts: Shoulds define the set (union of Should posting lists).
		q.Shoulds.Iter(s.Storage, func(state *ClauseState) {
			ctx.Bitmap.Or(&s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap)
		})
	}

	if mustNotsCount > 0 {
		// MustNots subtract from whatever the set is.
		q.MustNots.Iter(s.Storage, func(state *ClauseState) {
			ctx.Bitmap.AndNot(&s.Storage.PostingLists[state.Token.PostingListIndex].Bitmap)
		})
	}

	ctx.Scores = make(map[uint64]float64, ctx.Bitmap.GetCardinality())

	if mustsCount > 0 {
		q.Musts.Iter(s.Storage, func(state *ClauseState) { s.UpdateScores(ctx, state) })
	}
	if shouldsCount > 0 {
		q.Shoulds.Iter(s.Storage, func(state *ClauseState) { s.UpdateScores(ctx, state) })
	}
}

// Once a filtering is done scoring is the next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (s *Searcher) ResolveBM25(ctx *QueryContext) (idxs []uint64) {
	type bm25 struct {
		docIdx uint64
		score  float64
	}

	scores := make([]bm25, 0, ctx.Bitmap.GetCardinality())

	it := ctx.Bitmap.Iterator()
	for it.HasNext() {
		doxIdx := it.Next()

		score := ctx.Scores[doxIdx]
		if score == 0 {
			continue
		}

		scores = append(scores, bm25{
			score:  score,
			docIdx: doxIdx,
		})
	}

	slices.SortFunc(
		scores,
		func(a, b bm25) int {
			scoreCmp := cmp.Compare(b.score, a.score)
			if scoreCmp == 0 {
				return cmp.Compare(b.docIdx, a.docIdx)
			}
			return scoreCmp
		},
	)

	idxs = make([]uint64, len(scores))
	for index := range scores {
		idxs[index] = scores[index].docIdx
	}
	return idxs
}
