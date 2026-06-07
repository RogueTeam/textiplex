package query

import (
	"github.com/RogueTeam/textiplex/tuple"
)

func (s *Searcher) UpdateScores(ctx *QueryContext, state *ClauseState) {
	token := state.Token
	field := state.Field

	fieldsTokenDocsKey := tuple.Tuple3[uint64]{A: state.FieldHash, B: state.TokenHash}
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
