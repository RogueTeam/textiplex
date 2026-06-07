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
	FieldHash uint64
	Value     T
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
		FieldHash: field,
		Value: Keyword{
			Value: kw,
			Boost: boost,
		},
	})
}

func (c *Clause) FieldRange(field uint64, hi, lo []byte, boost float64) {
	c.FieldRanges = append(c.FieldRanges, &ClauseEntry[Range]{
		FieldHash: field,
		Value: Range{
			High:  hi,
			Low:   lo,
			Boost: boost,
		},
	})
}

type ClauseState struct {
	// Used to check if something was actuall found or not
	// Should always be handled first by caller
	Found bool
	Boost float64
	// Field references
	Field     *storage.Field
	FieldHash uint64
	// Token references
	Token *storage.Token
}

type HandleClauseFunc func(state *ClauseState)

func (s *Searcher) Iter(c *Clause, handle HandleClauseFunc) {
	var state ClauseState

	var tokensKey storage.Token
	for _, kw := range c.Keywords {
		state.Boost = kw.Boost
		tokensKey.Value = kw.Value

		var found bool
		for state.FieldHash, state.Field = range s.Storage.Fields {
			state.Token, state.Found = state.Field.Tokens.Get(&tokensKey)
			if !state.Found {
				continue
			}

			handle(&state)
			if !found {
				found = true
			}
		}

		// For those that were not found we need to do something
		if !found {
			state.Found = false
			handle(&state)
		}
	}

	for _, entry := range c.FieldKeywords {
		state.FieldHash = entry.FieldHash
		state.Boost = entry.Value.Boost

		state.Field, state.Found = s.Storage.Fields[entry.FieldHash]
		if !state.Found {
			handle(&state)
			continue
		}

		tokensKey.Value = entry.Value.Value
		state.Token, state.Found = state.Field.Tokens.Get(&tokensKey)

		handle(&state)
	}

	var tokenKey storage.Token
	for _, entry := range c.FieldRanges {
		state.FieldHash = entry.FieldHash
		state.Boost = entry.Value.Boost

		state.Field, state.Found = s.Storage.Fields[state.FieldHash]
		if !state.Found {
			handle(&state)
			continue
		}

		if state.Field.Tokens.Len() == 0 {
			state.Found = false
			handle(&state)
			continue
		}

		var (
			lo = entry.Value.Low
			hi = entry.Value.High
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

		var found bool
		tokenKey.Value = lo
		for valid := it.Seek(&tokenKey); valid; valid = it.Next() {
			state.Token = it.Item()
			if bytes.Compare(state.Token.Value, hi) > 0 {
				break
			}

			handle(&state)
			if !found {
				found = true
			}
		}
		it.Release()

		if !found {
			state.Found = false
			handle(&state)
		}
	}
}

// Query context intended to be cached and reused by caller on each search
type QueryContext struct {
	Bitmap roaring64.Bitmap
	Scores map[uint64]float64
}
