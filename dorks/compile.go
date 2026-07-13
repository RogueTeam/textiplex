package dorks

import (
	"iter"

	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/keyword"
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

	if defTokenizer == nil {
		return keyword.Tokenizer
	}
	return defTokenizer
}

const AllFieldNone = 0

// The all field permits dorks to focus search on a specific field when keywords are
// received without field
func (q *Query) Compile(allField uint64, defTokenizer tokenizer.Tokenizer, fieldsTokenizer map[uint64]tokenizer.Tokenizer) (sq *query.SimpleQuery) {
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

		var boost float32
		if dork.Boost != nil {
			boost = *dork.Boost
		} else {
			boost = 1.0
		}

		var fuzzy int
		if dork.Fuzzy != nil {
			fuzzy = *dork.Fuzzy
		}

		// 1. Bare keyword (no field, no value): free text, analyzed with the
		//    default tokenizer and expanded into one entry per produced term.
		if dork.Match == nil {
			if allField != 0 {
				for token := range TokenizeOrPushValue(defTokenizer, []byte(dork.Keyword)) {
					targetClause.FieldKeyword(allField, token.Value, boost, fuzzy)
				}
			} else {
				for token := range TokenizeOrPushValue(defTokenizer, []byte(dork.Keyword)) {
					targetClause.Keyword(token.Value, boost, fuzzy)
				}
			}
			continue
		}

		fieldHash := xxh3.HashString(string(dork.Keyword))
		match := dork.Match

		// 2. Structured values are NEVER analyzed: numbers and dates are
		//    sortable-encoded, and any range keeps its literal bound.
		tokenizer := PickTokenizer(defTokenizer, fieldsTokenizer, fieldHash)
		for tok := range tokenizer([]byte(match.Value)) {
			switch match.Operator {
			case MatchOperatorNone:
				targetClause.FieldKeyword(fieldHash, tok.Value, boost, fuzzy)
			case MatchOperatorGreater:
				targetClause.FieldRange(fieldHash, tok.Value, nil, query.RangeCaptureModeRight, boost)
			case MatchOperatorGreaterEqual:
				targetClause.FieldRange(fieldHash, tok.Value, nil, query.RangeCaptureModeBoth, boost)
			case MatchOperatorLess:
				targetClause.FieldRange(fieldHash, nil, tok.Value, query.RangeCaptureModeLeft, boost)
			case MatchOperatorLessEqual:
				targetClause.FieldRange(fieldHash, nil, tok.Value, query.RangeCaptureModeBoth, boost)
			}
		}
	}
	return sq
}
