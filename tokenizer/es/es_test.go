package es_test

import (
	"testing"

	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/es"
)

func TestSpanishStemming(t *testing.T) {
	type Test struct {
		name string
		in   string
		want []testsuite.Term
	}

	tests := []Test{
		{name: "plural s stripped borrowed", in: "casas", want: []testsuite.Term{{Value: "casa", Owned: false}}},
		{name: "singular unchanged borrowed", in: "casa", want: []testsuite.Term{{Value: "casa", Owned: false}}},
		{name: "es plural on consonant stem", in: "flores", want: []testsuite.Term{{Value: "flor", Owned: false}}},
		{name: "es plural longer word", in: "canciones", want: []testsuite.Term{{Value: "cancion", Owned: false}}},
		{name: "accent folded forces alloc", in: "canción", want: []testsuite.Term{{Value: "cancion", Owned: true}}},
		{name: "mente adverb plus accent fold", in: "rápidamente", want: []testsuite.Term{{Value: "rapida", Owned: true}}},
		{name: "enye preserved on plural", in: "niños", want: []testsuite.Term{{Value: "niño", Owned: true}}},
		{name: "es plural is lossy", in: "felices", want: []testsuite.Term{{Value: "felic", Owned: false}}},
		{name: "capital folded", in: "Las", want: []testsuite.Term{{Value: "las", Owned: true}}},
		{name: "short bare s is lossy", in: "tres", want: []testsuite.Term{{Value: "tre", Owned: false}}},
		{name: "three letter word untouched", in: "los", want: []testsuite.Term{{Value: "los", Owned: false}}},
		{name: "diaeresis folded", in: "pingüino", want: []testsuite.Term{{Value: "pinguino", Owned: true}}},
		{name: "a acute folded", in: "árbol", want: []testsuite.Term{{Value: "arbol", Owned: true}}},
		{name: "u acute folded", in: "según", want: []testsuite.Term{{Value: "segun", Owned: true}}},
		{name: "o acute folded", in: "corazón", want: []testsuite.Term{{Value: "corazon", Owned: true}}},
		{name: "lowercase word borrowed", in: "el", want: []testsuite.Term{{Value: "el", Owned: false}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsuite.AssertTerms(t, es.SpanishTokenizer, tc.in, tc.want)
		})
	}
}

// ── Ownership across a full sentence ──────────────────────────────────────────

func TestOwnership(t *testing.T) {
	type Test struct {
		name string
		fn   tokenizer.Tokenizer
		in   string
		want []testsuite.Term
	}

	tests := []Test{
		{
			name: "spanish sentence mixes borrow and owned",
			fn:   es.SpanishTokenizer,
			in:   "Los niños cantan",
			want: []testsuite.Term{{Value: "los", Owned: true}, {Value: "niño", Owned: true}, {Value: "cantan", Owned: false}},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsuite.AssertTerms(t, tc.fn, tc.in, tc.want)
		})
	}
}
