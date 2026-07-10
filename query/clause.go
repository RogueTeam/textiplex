package query

import (
	"bytes"

	"github.com/RoaringBitmap/roaring"
	"github.com/RogueTeam/textiplex/levenshtein"
	"github.com/RogueTeam/textiplex/storage"
)

type RangeCaptureMode int

const (
	RangeCaptureModeNone RangeCaptureMode = iota
	RangeCaptureModeLeft
	RangeCaptureModeRight
	RangeCaptureModeBoth
)

type Range struct {
	CaptureMode RangeCaptureMode
	Boost       float64
	Low, High   []byte
}

type Keyword struct {
	Boost float64
	Fuzzy int
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

func (c *Clause) Keyword(kw []byte, boost float64, fuzzy int) {
	c.Keywords = append(c.Keywords, &Keyword{
		Value: kw,
		Boost: boost,
		Fuzzy: fuzzy,
	})
}

func (c *Clause) FieldKeyword(field uint64, kw []byte, boost float64, fuzzy int) {
	c.FieldKeywords = append(c.FieldKeywords, &ClauseEntry[Keyword]{
		FieldHash: field,
		Value: Keyword{
			Value: kw,
			Boost: boost,
			Fuzzy: fuzzy,
		},
	})
}

func (c *Clause) FieldRange(field uint64, lo, hi []byte, mode RangeCaptureMode, boost float64) {
	c.FieldRanges = append(c.FieldRanges, &ClauseEntry[Range]{
		FieldHash: field,
		Value: Range{
			CaptureMode: mode,
			Low:         lo,
			High:        hi,
			Boost:       boost,
		},
	})
}

type ClauseState struct {
	Boost float64
	// Field references
	Field *storage.Field
	// Token references
	Tokens []*storage.Token
}

type HandleClauseFunc func(state *ClauseState)

func (s *Searcher) Iter(c *Clause, handle HandleClauseFunc) {
	var state ClauseState

	for _, kw := range c.Keywords {
		state.Boost = kw.Boost

		var found bool
		for _, state.Field = range s.Storage.Fields {
			token, tokenFound := state.Field.Tokens.GetBytes(kw.Value)
			if tokenFound {
				state.Tokens = state.Tokens[:0]
				state.Tokens = append(state.Tokens, token)

				handle(&state)
				if !found {
					found = true
				}
				continue
			}

			// Levenshtein use the fuzzyK of defined in the keyword
			k := min(s.LevenshteinMaxK, kw.Fuzzy)
			var m int
			if s.LevenshteinM != 0 {
				m = s.LevenshteinM
			} else {
				m = levenshtein.DefaultM
			}
			if k > 0 && m > 0 {
				automata := levenshtein.New(k, m, kw.Value, state.Field.Tokens)
				for token = range automata.Matches() {
					state.Tokens = state.Tokens[:0]
					state.Tokens = append(state.Tokens, token)

					if !found {
						found = true
					}
					handle(&state)
					break
				}
			}
		}

		// For those that were not found we need to do something
		if !found {
			state.Tokens = state.Tokens[:0]
			handle(&state)
		}
	}

fieldKwLoop:
	for _, entry := range c.FieldKeywords {
		state.Boost = entry.Value.Boost

		var fieldFound bool
		state.Field, fieldFound = s.Storage.Fields[entry.FieldHash]
		if !fieldFound {
			state.Tokens = state.Tokens[:0]
			handle(&state)
			continue
		}

		token, found := state.Field.Tokens.GetBytes(entry.Value.Value)
		if found {
			state.Tokens = state.Tokens[:0]
			state.Tokens = append(state.Tokens, token)
			handle(&state)
			continue
		}

		// Levenshtein use the fuzzyK of defined in the keyword
		k := min(s.LevenshteinMaxK, entry.Value.Fuzzy)
		var m int
		if s.LevenshteinM != 0 {
			m = s.LevenshteinM
		} else {
			m = levenshtein.DefaultM
		}
		if k > 0 && m > 0 {
			automata := levenshtein.New(k, m, entry.Value.Value, state.Field.Tokens)
			for token = range automata.Matches() {
				state.Tokens = state.Tokens[:0]
				state.Tokens = append(state.Tokens, token)

				handle(&state)
				continue fieldKwLoop
			}
		}

		// If everything fail, send state with nothing
		state.Tokens = state.Tokens[:0]
		handle(&state)
	}

	for _, entry := range c.FieldRanges {
		state.Boost = entry.Value.Boost

		var fieldFound bool
		state.Field, fieldFound = s.Storage.Fields[entry.FieldHash]
		if !fieldFound {
			state.Tokens = state.Tokens[:0]
			handle(&state)
			continue
		}

		var (
			lo = entry.Value.Low
			hi = entry.Value.High
		)
		if len(lo) == 0 {
			tok := state.Field.Tokens[0]
			lo = tok.Value.Bytes()
		}
		if len(hi) == 0 {
			tok := state.Field.Tokens[len(state.Field.Tokens)-1]
			hi = tok.Value.Bytes()
		}

		var first bool

		state.Tokens = state.Tokens[:0]
		for token := range state.Field.Tokens.IterBytes(lo, hi) {
			if !first {
				first = true

				tokLoCmp := bytes.Compare(token.Value.Bytes(), lo)

				// Only ignore the first element if it is equal to the lo end and capture mode is set to > or ><
				if tokLoCmp == 0 && (entry.Value.CaptureMode == RangeCaptureModeRight || entry.Value.CaptureMode == RangeCaptureModeNone) {
					continue
				}
			}

			tokHiCmp := bytes.Compare(token.Value.Bytes(), hi)
			if tokHiCmp == 1 || (tokHiCmp == 0 && (entry.Value.CaptureMode == RangeCaptureModeLeft || entry.Value.CaptureMode == RangeCaptureModeNone)) {
				break
			}

			state.Tokens = append(state.Tokens, token)
		}

		handle(&state)
	}
}

// Query context intended to be cached and reused by caller on each search
type QueryContext struct {
	Bitmap roaring.Bitmap
	Scores map[uint32]float64
}
