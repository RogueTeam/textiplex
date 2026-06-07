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
	// Used to check if something was actuall found or not
	// Should always be handled first by caller
	Found bool

	Boost float64

	// Token references
	Token     *storage.Token
	TokenHash uint64

	// Field references
	Field     *storage.Field
	FieldHash uint64
}

type HandleClauseFunc func(state *ClauseState)

func (s *Searcher) Iter(c *Clause, handle HandleClauseFunc) {
	var state ClauseState

	for _, kw := range c.Keywords {
		state.TokenHash = kw.Hash
		state.Boost = kw.Boost

		var fields []*DirectTokenFieldReference
		fields, state.Found = s.TokenFields[state.TokenHash]
		if !state.Found {
			handle(&state)
			continue
		}

		for _, entry := range fields {
			state.Field = entry.Field
			state.FieldHash = entry.FieldHash
			state.Token = entry.Token

			handle(&state)
		}
	}

	var fieldTokenKey tuple.Tuple2[uint64]
	for _, entry := range c.FieldKeywords {
		state.Boost = entry.Value.Boost
		state.FieldHash = entry.Field
		state.TokenHash = entry.Value.Hash

		fieldTokenKey.A = entry.Field
		fieldTokenKey.B = state.TokenHash

		var ref *DirectTokenFieldReference
		ref, state.Found = s.FieldTokens[fieldTokenKey.Hash()]
		if !state.Found {
			handle(&state)
			continue
		}

		state.Field = ref.Field
		state.Token = ref.Token

		handle(&state)
	}

	var tokenKey storage.Token
	for _, entry := range c.FieldRanges {
		state.FieldHash = entry.Field
		state.Boost = entry.Value.Boost

		var found bool
		state.Field, found = s.Storage.Fields[state.FieldHash]
		if !found {
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
