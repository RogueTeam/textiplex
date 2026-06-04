package query

import (
	"bytes"

	"github.com/RogueTeam/textiplex/storage"
)

type Range struct {
	Boost     float64
	Low, High []byte
}

type Keyword struct {
	Boost float64
	Value []byte
}

type Clause struct {
	Keywords      []*Keyword
	FieldKeywords map[uint64]*Keyword
	FieldRanges   map[uint64]*Range
}

func (c *Clause) Count() (count int) {
	return len(c.Keywords) + len(c.FieldKeywords) + len(c.FieldRanges)
}

func (c *Clause) Keyword(kw []byte, boost float64) {
	c.Keywords = append(c.Keywords, &Keyword{
		Value: kw,
		Boost: boost,
	})
}

func (c *Clause) FieldKeyword(field uint64, kw []byte, boost float64) {
	if c.FieldKeywords == nil {
		c.FieldKeywords = map[uint64]*Keyword{
			field: &Keyword{
				Value: kw,
				Boost: boost,
			},
		}
	} else {
		c.FieldKeywords[field] = &Keyword{
			Value: kw,
			Boost: boost,
		}
	}
}

func (c *Clause) FieldRange(field uint64, hi, lo []byte, boost float64) {
	if c.FieldRanges == nil {
		c.FieldRanges = map[uint64]*Range{
			field: &Range{
				High:  hi,
				Low:   lo,
				Boost: boost,
			},
		}
	} else {
		c.FieldRanges[field] = &Range{
			High:  hi,
			Low:   lo,
			Boost: boost,
		}
	}
}

type ClauseState struct {
	Keyword   *Keyword
	Range     *Range
	Token     *storage.Token
	FieldHash uint64
	Field     *storage.Field
}

type HandleClauseFunc func(state *ClauseState)

func (c *Clause) Iter(ctx *QueryContext, s *storage.Storage, handle HandleClauseFunc) {
	var state ClauseState

	var tokenKey storage.Token

	for _, state.Keyword = range c.Keywords {
		tokenKey.Value = state.Keyword.Value
		for _, state.Field = range s.Fields {
			var found bool
			state.Token, found = state.Field.Tokens.Get(&tokenKey)
			if !found {
				continue
			}

			handle(&state)
		}
	}

	for state.FieldHash, state.Keyword = range c.FieldKeywords {
		var found bool
		state.Field, found = s.Fields[state.FieldHash]
		if !found {
			continue
		}
		tokenKey.Value = state.Keyword.Value
		state.Token, found = state.Field.Tokens.Get(&tokenKey)
		if !found {
			continue
		}

		handle(&state)
	}

	for state.FieldHash, state.Range = range c.FieldRanges {
		var found bool
		state.Field, found = s.Fields[state.FieldHash]
		if !found {
			continue
		}

		if state.Field.Tokens.Len() == 0 {
			continue
		}

		var (
			lo = state.Range.Low
			hi = state.Range.High
		)
		if len(lo) == 0 {
			tok, _ := state.Field.Tokens.GetAt(0)
			lo = tok.Value
		}
		if len(hi) == 0 {
			tok, _ := state.Field.Tokens.GetAt(state.Field.Tokens.Len() - 1)
			hi = tok.Value
		}
		it := state.Field.Tokens.Iter()

		tokenKey.Value = lo
		for valid := it.Seek(&tokenKey); valid; valid = it.Next() {
			state.Token = it.Item()
			if bytes.Compare(state.Token.Value, hi) > 0 {
				break
			}

			handle(&state)
		}
		it.Release()
	}
}
