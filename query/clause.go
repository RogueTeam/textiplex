package query

import (
	"bytes"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tuple"
	"github.com/zeebo/xxh3"
)

type Range struct {
	Boost     float64
	Low, High []byte
}

type Keyword struct {
	Boost float64
	Value []byte
	Hash  uint64
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
		Hash:  xxh3.Hash(kw),
		Boost: boost,
	})
}

func (c *Clause) FieldKeyword(field uint64, kw []byte, boost float64) {
	c.FieldKeywords = append(c.FieldKeywords, &ClauseEntry[Keyword]{
		Field: field,
		Value: Keyword{
			Value: kw,
			Hash:  xxh3.Hash(kw),
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
	Keyword *Keyword
	Range   *Range

	Token     *storage.Token
	TokenHash uint64

	Field     *storage.Field
	FieldHash uint64
}

type HandleClauseFunc func(state *ClauseState)

func (s *Searcher) Iter(c *Clause, handle HandleClauseFunc) {
	var state ClauseState

	for _, state.Keyword = range c.Keywords {
		state.TokenHash = state.Keyword.Hash
		for _, entry := range s.TokenFields[state.TokenHash] {
			state.Field = entry.Field
			state.FieldHash = entry.FieldHash
			state.Token = entry.Token

			handle(&state)
		}
	}

	var fieldTokenKey tuple.Tuple2[uint64]
	for _, entry := range c.FieldKeywords {
		state.Keyword = &entry.Value
		state.FieldHash = entry.Field
		state.TokenHash = entry.Value.Hash

		fieldTokenKey.A = entry.Field
		fieldTokenKey.B = state.TokenHash

		ref, found := s.FieldTokens[fieldTokenKey.Hash()]
		if !found {
			continue
		}

		state.Field = ref.Field
		state.Token = ref.Token

		handle(&state)
	}

	var tokenKey storage.Token
	for _, entry := range c.FieldRanges {
		state.FieldHash = entry.Field
		state.Range = &entry.Value

		var found bool
		state.Field, found = s.Storage.Fields[state.FieldHash]
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
			state.TokenHash = xxh3.Hash(state.Token.Value)
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
