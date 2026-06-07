package query

import (
	"github.com/RogueTeam/textiplex/tuple"
	"github.com/zeebo/xxh3"
)

func (s *Searcher) UpdateScores(ctx *QueryContext, state *ClauseState) {
	if !state.Found {
		return
	}
	token := state.Token
	field := state.Field

	fieldsTokenDocsKey := tuple.Tuple3[uint64]{A: state.FieldHash, B: xxh3.Hash(token.Value)}
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

		ctx.Scores[docIdx] += state.Boost * ScoreTermBM25(
			/* docCoun */ uint64(len(field.DocumentLengths)),
			/* tokenDocFreq */ token.FrequencyCount,
			/* tokenFreq */ freq,
			/* documentLength */ docLength,
			/* avgDocLength */ field.AvgDocumentLength,
			/* saturation */ DefaultSaturation,
			/* lengthPenalty */ DefaultLengthPenalty,
		)
	}
}
