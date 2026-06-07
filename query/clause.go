package query

import (
	"bytes"

	"github.com/RoaringBitmap/roaring/roaring64"
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

type ClauseEntry[T Keyword | Range] struct {
	Field uint64
	Value T
}

type Clause struct {
	Keywords      []*Keyword
	FieldKeywords []*ClauseEntry[Keyword]
	FieldRanges   []*ClauseEntry[Range]
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
	c.FieldKeywords = append(c.FieldKeywords, &ClauseEntry[Keyword]{
		Field: field,
		Value: Keyword{
			Value: kw,
			Boost: boost,
		},
	})
}

func (c *Clause) FieldRange(field uint64, hi, lo []byte, boost float64) {
	c.FieldRanges = append(c.FieldRanges, &ClauseEntry[Range]{
		Field: field,
		Value: Range{
			High:  hi,
			Low:   lo,
			Boost: boost,
		},
	})
}

type ClauseState struct {
	Keyword   *Keyword
	Range     *Range
	Token     *storage.Token
	FieldHash uint64
	Field     *storage.Field
}

type HandleClauseFunc func(state *ClauseState)

func (c *Clause) Iter(s *storage.Storage, handle HandleClauseFunc) {
	var state ClauseState

	var tokenKey storage.Token

	for _, state.Keyword = range c.Keywords {
		tokenKey.Value = state.Keyword.Value
		for state.FieldHash, state.Field = range s.Fields {
			var found bool
			state.Token, found = state.Field.Tokens.Get(&tokenKey)
			if !found {
				continue
			}

			handle(&state)
		}
	}

	for _, entry := range c.FieldKeywords {
		state.FieldHash = entry.Field
		state.Keyword = &entry.Value

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

	for _, entry := range c.FieldRanges {
		state.FieldHash = entry.Field
		state.Range = &entry.Value

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

// Query context intended to be cached and reused by caller on each search
type QueryContext struct {
	Bitmap roaring64.Bitmap
	Scores map[uint64]float64
}
