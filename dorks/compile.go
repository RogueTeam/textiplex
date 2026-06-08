package dorks

import (
	"iter"

	"github.com/RogueTeam/textiplex/numeric"
	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/zeebo/xxh3"
)

func TokenizeOrPushValue(tok tokenizer.Tokenizer, value []byte) (seq iter.Seq[*tokenizer.Token]) {
	if tok == nil {
		return func(yield func(*tokenizer.Token) bool) {
			yield(&tokenizer.Token{Value: value})
		}
	}
	return tok(value)
}

func PickTokenizer(defTokenizer tokenizer.Tokenizer, fieldsTokenizer map[uint64]tokenizer.Tokenizer, fieldHash uint64) (toknizer tokenizer.Tokenizer) {
	if fieldsTokenizer != nil {
		if ft, ok := fieldsTokenizer[fieldHash]; ok {
			return ft
		}
	}
	return defTokenizer
}

func (q *Query) Compile(defTokenizer tokenizer.Tokenizer, fieldsTokenizer map[uint64]tokenizer.Tokenizer) (sq *query.SimpleQuery) {
	sq = new(query.SimpleQuery)

	for _, dork := range q.Dorks {
		// The clause bucket is chosen once per dork. Analysis happens for every
		// bucket, Musts included: the index stores analyzed tokens, so an
		// unanalyzed term misses the index, and for a Must that makes
		// FilterDocuments clear the entire result set.
		var targetClause *query.Clause
		switch dork.Operator {
		case OperatorMust:
			targetClause = &sq.Musts
		case OperatorMustNot:
			targetClause = &sq.MustNots
		default: // OperatorNone
			targetClause = &sq.Shoulds
		}

		// 1. Bare keyword (no field, no value): free text, analyzed with the
		//    default tokenizer and expanded into one entry per produced term.
		if dork.Match == nil {
			for token := range TokenizeOrPushValue(defTokenizer, []byte(dork.Keyword)) {
				targetClause.Keyword(token.Value, 1.0)
			}
			continue
		}

		fieldHash := xxh3.HashString(string(dork.Keyword))
		match := dork.Match

		boost := 1.0
		if match.Boost != nil {
			boost = *match.Boost
		}

		// 2. Structured values are NEVER analyzed: numbers and dates are
		//    sortable-encoded, and any range keeps its literal bound.
		var data []byte
		switch {
		case match.Date != nil:
			data = make([]byte, 8)
			numeric.PutSortableInteger(data, match.Date.Value.UnixNano())
		case match.Integer != nil:
			data = make([]byte, 8)
			numeric.PutSortableInteger(data, match.Integer.Value)
		case match.Float != nil:
			data = make([]byte, 8)
			numeric.PutSortableFloat(data, match.Float.Value)
		case match.Keyword != nil:
			// A keyword-valued equality match (field:value) is analyzed with the
			// field's tokenizer (default if none) and expanded. A keyword RANGE
			// (field:>value) keeps its literal lexicographic bound, so it falls
			// through to the range handling below unanalyzed.
			if match.Operator == MatchOperatorNone {
				toknizer := PickTokenizer(defTokenizer, fieldsTokenizer, fieldHash)
				for token := range TokenizeOrPushValue(toknizer, []byte(*match.Keyword)) {
					targetClause.FieldKeyword(fieldHash, token.Value, boost)
				}
				continue
			}
			data = []byte(*match.Keyword)
		}

		switch match.Operator {
		case MatchOperatorNone:
			targetClause.FieldKeyword(fieldHash, data, boost)
		case MatchOperatorGreater:
			targetClause.FieldRange(fieldHash, data, nil, query.RangeCaptureModeRight, boost)
		case MatchOperatorGreaterEqual:
			targetClause.FieldRange(fieldHash, data, nil, query.RangeCaptureModeBoth, boost)
		case MatchOperatorLess:
			targetClause.FieldRange(fieldHash, nil, data, query.RangeCaptureModeLeft, boost)
		case MatchOperatorLessEqual:
			targetClause.FieldRange(fieldHash, nil, data, query.RangeCaptureModeBoth, boost)
		}
	}
	return sq
}
