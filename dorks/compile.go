package dorks

import (
	"github.com/RogueTeam/textiplex/numeric"
	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/zeebo/xxh3"
)

func (q *Query) Compile(defTokenizer tokenizer.Tokenizer, fieldsTokenizer map[uint64]tokenizer.Tokenizer) (sq *query.SimpleQuery) {
	sq = new(query.SimpleQuery)
	for _, dork := range q.Dorks {
		if dork.Match == nil {
			switch dork.Operator {
			case OperatorMust:
				sq.Musts.Keyword([]byte(dork.Keyword), 1.0)
			case OperatorMustNot:
				sq.MustNots.Keyword([]byte(dork.Keyword), 1.0)
			case OperatorNone:
				sq.Shoulds.Keyword([]byte(dork.Keyword), 1.0)
			}
			continue
		}

		fieldHash := xxh3.HashString(string(dork.Keyword))

		var data []byte
		match := dork.Match
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
			data = []byte(*match.Keyword)
		}

		var targetClause *query.Clause
		switch dork.Operator {
		case OperatorMust:
			targetClause = &sq.Musts
		case OperatorMustNot:
			targetClause = &sq.MustNots
		case OperatorNone:
			targetClause = &sq.Shoulds
		}

		var boost float64
		if match.Boost != nil {
			boost = *match.Boost
		} else {
			boost = 1.0
		}

		switch match.Operator {
		case MatchOperatorNone:
			targetClause.FieldKeyword(fieldHash, data, boost)
		case MatchOperatorGreater, MatchOperatorGreaterEqual:
			targetClause.FieldRange(fieldHash, nil, data, boost)
		case MatchOperatorLess, MatchOperatorLessEqual:
			targetClause.FieldRange(fieldHash, data, nil, boost)
		}
	}
	return sq
}
