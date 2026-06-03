package en_test

import (
	"testing"

	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/en"
)

// ── Splitting and punctuation ─────────────────────────────────────────────────

func TestSplitting(t *testing.T) {
	type Test struct {
		name string
		in   string
		want []testsuite.Term
	}

	tests := []Test{
		{name: "empty input yields nothing", in: "", want: nil},
		{name: "whitespace only yields nothing", in: "   \t\n ", want: nil},
		{name: "punctuation only yields nothing", in: ".,!?[]{}()", want: nil},
		{name: "two words split on space", in: "foo bar", want: []testsuite.Term{{Value: "foo", Owned: false}, {Value: "bar", Owned: false}}},
		{name: "brackets and commas stripped", in: "[foo], {bar}.", want: []testsuite.Term{{Value: "foo", Owned: false}, {Value: "bar", Owned: false}}},
		{name: "leading and trailing dots stripped", in: "...foo...", want: []testsuite.Term{{Value: "foo", Owned: false}}},
		{name: "hyphen between letters splits", in: "well-known", want: []testsuite.Term{{Value: "well", Owned: false}, {Value: "known", Owned: false}}},
		{name: "acronym dots split into letters", in: "U.S.A", want: []testsuite.Term{{Value: "u", Owned: true}, {Value: "s", Owned: true}, {Value: "a", Owned: true}}},
		{name: "letter digit run stays joined", in: "covid19", want: []testsuite.Term{{Value: "covid19", Owned: false}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsuite.AssertTerms(t, en.Tokenizer, tc.in, tc.want)
		})
	}
}

// ── Numbers ───────────────────────────────────────────────────────────────────

func TestNumbers(t *testing.T) {
	type Test struct {
		name string
		in   string
		want []testsuite.Term
	}

	tests := []Test{
		{name: "decimal point kept", in: "3.14", want: []testsuite.Term{{Value: "3.14", Owned: false}}},
		{name: "thousands separators kept", in: "1.250.000", want: []testsuite.Term{{Value: "1.250.000", Owned: false}}},
		{name: "iso date kept whole", in: "2025-06-03", want: []testsuite.Term{{Value: "2025-06-03", Owned: false}}},
		{name: "slash date kept whole", in: "12/06/2025", want: []testsuite.Term{{Value: "12/06/2025", Owned: false}}},
		{name: "time colon kept", in: "12:30", want: []testsuite.Term{{Value: "12:30", Owned: false}}},
		{name: "chained separators kept", in: "1.2.3", want: []testsuite.Term{{Value: "1.2.3", Owned: false}}},
		{name: "trailing separator dropped", in: "3.", want: []testsuite.Term{{Value: "3", Owned: false}}},
		{name: "separator before letter splits", in: "3.a", want: []testsuite.Term{{Value: "3", Owned: false}, {Value: "a", Owned: false}}},
		{name: "number embedded in words", in: "year 2025 ok", want: []testsuite.Term{{Value: "year", Owned: false}, {Value: "2025", Owned: false}, {Value: "ok", Owned: false}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsuite.AssertTerms(t, en.Tokenizer, tc.in, tc.want)
		})
	}
}

// ── English stemming ──────────────────────────────────────────────────────────

func TestEnglishStemming(t *testing.T) {
	type Test struct {
		name string
		in   string
		want []testsuite.Term
	}

	tests := []Test{
		{name: "plural s stripped and borrowed", in: "cats", want: []testsuite.Term{{Value: "cat", Owned: false}}},
		{name: "lowercase unchanged stays borrowed", in: "the", want: []testsuite.Term{{Value: "the", Owned: false}}},
		{name: "capital folded forces alloc", in: "The", want: []testsuite.Term{{Value: "the", Owned: true}}},
		{name: "ing with undouble and capital", in: "Running", want: []testsuite.Term{{Value: "run", Owned: true}}},
		{name: "ing with undouble lowercase borrowed", in: "running", want: []testsuite.Term{{Value: "run", Owned: false}}},
		{name: "ed with undouble", in: "stopped", want: []testsuite.Term{{Value: "stop", Owned: false}}},
		{name: "ed without undouble", in: "hoped", want: []testsuite.Term{{Value: "hop", Owned: false}}},
		{name: "ed guarded on short word", in: "bed", want: []testsuite.Term{{Value: "bed", Owned: false}}},
		{name: "ly stripped", in: "quickly", want: []testsuite.Term{{Value: "quick", Owned: false}}},
		{name: "es stripped", in: "boxes", want: []testsuite.Term{{Value: "box", Owned: false}}},
		{name: "sses to ss", in: "kisses", want: []testsuite.Term{{Value: "kiss", Owned: false}}},
		{name: "ies stripped", in: "ponies", want: []testsuite.Term{{Value: "poni", Owned: false}}},
		{name: "trailing ss not stripped", in: "press", want: []testsuite.Term{{Value: "press", Owned: false}}},
		{name: "short bare s is lossy", in: "bus", want: []testsuite.Term{{Value: "bu", Owned: false}}},
		{name: "two letter word untouched", in: "is", want: []testsuite.Term{{Value: "is", Owned: false}}},
		{name: "all caps folded", in: "HELLO", want: []testsuite.Term{{Value: "hello", Owned: true}}},
		{name: "alnum lowered and owned", in: "ABC123", want: []testsuite.Term{{Value: "abc123", Owned: true}}},
		{name: "accent kept in english fold", in: "café", want: []testsuite.Term{{Value: "café", Owned: true}}},
		{name: "pure digits untouched", in: "2025", want: []testsuite.Term{{Value: "2025", Owned: false}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsuite.AssertTerms(t, en.Tokenizer, tc.in, tc.want)
		})
	}
}

func TestOwnership(t *testing.T) {
	type Test struct {
		name string
		fn   tokenizer.Tokenizer
		in   string
		want []testsuite.Term
	}

	tests := []Test{
		{
			name: "english sentence mixes borrow and owned",
			fn:   en.Tokenizer,
			in:   "The cats ran",
			want: []testsuite.Term{{Value: "the", Owned: true}, {Value: "cat", Owned: false}, {Value: "ran", Owned: false}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsuite.AssertTerms(t, tc.fn, tc.in, tc.want)
		})
	}
}
