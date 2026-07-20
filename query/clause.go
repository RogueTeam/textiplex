package query

import (
	"bytes"
	"slices"

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
	Boost       float32
	Low, High   []byte
}

type Keyword struct {
	Boost float32
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

func (c *Clause) Keyword(kw []byte, boost float32, fuzzy int) {
	c.Keywords = append(c.Keywords, &Keyword{
		Value: kw,
		Boost: boost,
		Fuzzy: fuzzy,
	})
}

func (c *Clause) FieldKeyword(field uint64, kw []byte, boost float32, fuzzy int) {
	c.FieldKeywords = append(c.FieldKeywords, &ClauseEntry[Keyword]{
		FieldHash: field,
		Value: Keyword{
			Value: kw,
			Boost: boost,
			Fuzzy: fuzzy,
		},
	})
}

func (c *Clause) FieldRange(field uint64, lo, hi []byte, mode RangeCaptureMode, boost float32) {
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
	Boost float32
	// Field references
	Field *storage.Field
	// Token references
	Tokens []*storage.Token
}

type HandleClauseFunc func(state *ClauseState)

// Iterates about the matching fields + Tokens ignoring those entries that doesn't contain any token
func (s *Searcher) Iter(c *Clause, handle HandleClauseFunc) {
	var state ClauseState

	for _, kw := range c.Keywords {
		state.Boost = kw.Boost

		for _, state.Field = range s.Storage.Fields {
			state.Tokens = state.Tokens[:0]
			token, tokenFound := state.Field.Tokens.GetBytes(kw.Value)

			if tokenFound {
				state.Tokens = append(state.Tokens, token)
			} else {
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
						state.Tokens = append(state.Tokens, token)
					}
				}
			}

			if len(state.Tokens) != 0 {
				handle(&state)
			}
		}
	}

	for _, entry := range c.FieldKeywords {
		state.Tokens = state.Tokens[:0]
		state.Boost = entry.Value.Boost

		var fieldFound bool
		state.Field, fieldFound = s.Storage.Fields[entry.FieldHash]
		if fieldFound {
			token, found := state.Field.Tokens.GetBytes(entry.Value.Value)
			if found {
				state.Tokens = append(state.Tokens, token)
			} else {
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
						state.Tokens = append(state.Tokens, token)
					}
				}
			}
		}

		if len(state.Tokens) != 0 {
			handle(&state)
		}
	}

	for _, entry := range c.FieldRanges {
		state.Tokens = state.Tokens[:0]
		state.Boost = entry.Value.Boost

		var fieldFound bool
		state.Field, fieldFound = s.Storage.Fields[entry.FieldHash]
		if fieldFound {
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
		}
		if len(state.Tokens) != 0 {
			handle(&state)
		}
	}
}

type HandleClauseCondFunc func(state *ClauseState) (next bool)

// Same as Iter but also call handle when there is no tokens available for the clause.
// The return value of handle is used to early stop the query
func (s *Searcher) IterCond(c *Clause, handle HandleClauseCondFunc) {
	var state ClauseState

	for _, kw := range c.Keywords {
		state.Boost = kw.Boost

		var found bool
		for _, state.Field = range s.Storage.Fields {
			state.Tokens = state.Tokens[:0]
			token, tokenFound := state.Field.Tokens.GetBytes(kw.Value)

			if tokenFound {
				state.Tokens = append(state.Tokens, token)
			} else {
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
						state.Tokens = append(state.Tokens, token)
					}
				}
			}

			if len(state.Tokens) == 0 {
				continue
			}

			if !found {
				found = true
			}
			if !handle(&state) {
				return
			}
		}

		// For those that were not found we need to do something
		if !found {
			state.Field = nil
			state.Tokens = state.Tokens[:0]
			if !handle(&state) {
				return
			}
		}
	}

	for _, entry := range c.FieldKeywords {
		state.Tokens = state.Tokens[:0]
		state.Boost = entry.Value.Boost

		var fieldFound bool
		state.Field, fieldFound = s.Storage.Fields[entry.FieldHash]
		if fieldFound {
			token, found := state.Field.Tokens.GetBytes(entry.Value.Value)
			if found {
				state.Tokens = append(state.Tokens, token)
			} else {
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
						state.Tokens = append(state.Tokens, token)
					}
				}
			}
		}

		if !handle(&state) {
			return
		}
	}

	for _, entry := range c.FieldRanges {
		state.Tokens = state.Tokens[:0]
		state.Boost = entry.Value.Boost

		var fieldFound bool
		state.Field, fieldFound = s.Storage.Fields[entry.FieldHash]
		if fieldFound {
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
		}
		if !handle(&state) {
			return
		}
	}
}

type Scoring struct {
	Candidates []uint32
	Scores     []float32
}

func (s *Scoring) Reset(src *roaring.Bitmap) {
	s.Candidates = src.ToArray()
	s.Scores = make([]float32, len(s.Candidates))
}

func (s *Scoring) Resolve() (idxs []uint32) {
	return
}

func (s *Scoring) Add(guess int, idx uint32, score float32) (i int) {
	i, _ = slices.BinarySearch(s.Candidates[guess:], idx)
	s.Scores[guess:][i] += score
	return guess + i
}

func (s *Scoring) Get(guess int, idx uint32) (score float32) {
	i, found := slices.BinarySearch(s.Candidates[guess:], idx)
	if found {
		return s.Scores[guess:][i]
	}
	return 0
}

func (s *Scoring) Len() (n int) { return len(s.Candidates) }

// Query context intended to be cached and reused by caller on each search
type QueryContext struct {
	Bitmap  roaring.Bitmap
	Scoring Scoring
}
