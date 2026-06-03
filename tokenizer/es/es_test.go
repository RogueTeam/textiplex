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
		// ── singular unchanged ────────────────────────────────────────────────
		{name: "singular unchanged borrowed", in: "casa", want: []testsuite.Term{{Value: "casa", Owned: false}}},
		{name: "lowercase word borrowed", in: "el", want: []testsuite.Term{{Value: "el", Owned: false}}},
		{name: "short invariant words untouched", in: "con", want: []testsuite.Term{{Value: "con", Owned: false}}},
		{name: "ends in vowel no rule", in: "contrato", want: []testsuite.Term{{Value: "contrato", Owned: false}}},
		{name: "ends in vowel longer word", in: "gobierno", want: []testsuite.Term{{Value: "gobierno", Owned: false}}},
		{name: "ends in vowel domain word", in: "proceso", want: []testsuite.Term{{Value: "proceso", Owned: false}}},
		{name: "ends in consonant not s", in: "ciudad", want: []testsuite.Term{{Value: "ciudad", Owned: false}}},
		{name: "ends in z not stripped", in: "paz", want: []testsuite.Term{{Value: "paz", Owned: false}}},
		{name: "ends in d not stripped", in: "red", want: []testsuite.Term{{Value: "red", Owned: false}}},
		{name: "ends in l not stripped", in: "sol", want: []testsuite.Term{{Value: "sol", Owned: false}}},

		// ── bare s plural (n > 3) ─────────────────────────────────────────────
		{name: "plural s stripped borrowed", in: "casas", want: []testsuite.Term{{Value: "casa", Owned: false}}},
		{name: "plural s on libro", in: "libros", want: []testsuite.Term{{Value: "libro", Owned: false}}},
		{name: "plural s on mesa", in: "mesas", want: []testsuite.Term{{Value: "mesa", Owned: false}}},
		{name: "plural s on documento", in: "documentos", want: []testsuite.Term{{Value: "documento", Owned: false}}},
		{name: "plural s on contrato", in: "contratos", want: []testsuite.Term{{Value: "contrato", Owned: false}}},
		{name: "plural s on municipio", in: "municipios", want: []testsuite.Term{{Value: "municipio", Owned: false}}},
		{name: "plural s on otro", in: "otros", want: []testsuite.Term{{Value: "otro", Owned: false}}},
		{name: "plural s on nuevo", in: "nuevos", want: []testsuite.Term{{Value: "nuevo", Owned: false}}},
		{name: "plural s on mismo", in: "mismos", want: []testsuite.Term{{Value: "mismo", Owned: false}}},
		{name: "plural s on nuestro", in: "nuestros", want: []testsuite.Term{{Value: "nuestro", Owned: false}}},
		{name: "plural s on domain contratistas", in: "contratistas", want: []testsuite.Term{{Value: "contratista", Owned: false}}},
		{name: "plural s on domain funcionarios", in: "funcionarios", want: []testsuite.Term{{Value: "funcionario", Owned: false}}},
		// n <= 3: bare s guard protects short words
		{name: "three letter word untouched", in: "los", want: []testsuite.Term{{Value: "los", Owned: false}}},
		{name: "three letter guard gas", in: "gas", want: []testsuite.Term{{Value: "gas", Owned: false}}},
		{name: "three letter guard dos", in: "dos", want: []testsuite.Term{{Value: "dos", Owned: false}}},
		{name: "three letter guard mes", in: "mes", want: []testsuite.Term{{Value: "mes", Owned: false}}},
		{name: "three letter guard nos", in: "nos", want: []testsuite.Term{{Value: "nos", Owned: false}}},
		// lossy: short words with s at n==4 are clipped
		{name: "short bare s is lossy", in: "tres", want: []testsuite.Term{{Value: "tre", Owned: false}}},

		// ── es plural (n > 4) ─────────────────────────────────────────────────
		{name: "es plural on consonant stem", in: "flores", want: []testsuite.Term{{Value: "flor", Owned: false}}},
		{name: "es plural longer word", in: "canciones", want: []testsuite.Term{{Value: "cancion", Owned: false}}},
		{name: "es plural on ciudad", in: "ciudades", want: []testsuite.Term{{Value: "ciudad", Owned: false}}},
		{name: "es plural on entidad", in: "entidades", want: []testsuite.Term{{Value: "entidad", Owned: false}}},
		{name: "es plural on mujer", in: "mujeres", want: []testsuite.Term{{Value: "mujer", Owned: false}}},
		{name: "es plural on mes", in: "meses", want: []testsuite.Term{{Value: "mes", Owned: false}}},
		{name: "es plural domain licitaciones", in: "licitaciones", want: []testsuite.Term{{Value: "licitacion", Owned: false}}},
		// lossy: es rule strips exactly 2 bytes regardless of stem quality
		{name: "es plural lossy felic", in: "felices", want: []testsuite.Term{{Value: "felic", Owned: false}}},
		{name: "es plural lossy hombr", in: "hombres", want: []testsuite.Term{{Value: "hombr", Owned: false}}},
		// es plural + accent fold: accent was in the stem, forces alloc
		{name: "es plural accent on pais", in: "países", want: []testsuite.Term{{Value: "pais", Owned: true}}},
		{name: "es plural accent on arbol", in: "árboles", want: []testsuite.Term{{Value: "arbol", Owned: true}}},

		// ── mente adverb (n > 6) ─────────────────────────────────────────────
		{name: "mente adverb clara", in: "claramente", want: []testsuite.Term{{Value: "clara", Owned: false}}},
		{name: "mente adverb final", in: "finalmente", want: []testsuite.Term{{Value: "final", Owned: false}}},
		{name: "mente adverb total", in: "totalmente", want: []testsuite.Term{{Value: "total", Owned: false}}},
		{name: "mente adverb normal", in: "normalmente", want: []testsuite.Term{{Value: "normal", Owned: false}}},
		{name: "mente adverb amable", in: "amablemente", want: []testsuite.Term{{Value: "amable", Owned: false}}},
		{name: "mente adverb dura", in: "duramente", want: []testsuite.Term{{Value: "dura", Owned: false}}},
		{name: "mente adverb breve", in: "brevemente", want: []testsuite.Term{{Value: "breve", Owned: false}}},
		// mente + accent fold forces alloc
		{name: "mente adverb plus accent fold", in: "rápidamente", want: []testsuite.Term{{Value: "rapida", Owned: true}}},
		{name: "mente accent on facilmente", in: "fácilmente", want: []testsuite.Term{{Value: "facil", Owned: true}}},
		{name: "mente accent on unicamente", in: "únicamente", want: []testsuite.Term{{Value: "unica", Owned: true}}},
		{name: "mente accent on practicamente", in: "prácticamente", want: []testsuite.Term{{Value: "practica", Owned: true}}},
		{name: "mente accent on publicamente", in: "públicamente", want: []testsuite.Term{{Value: "publica", Owned: true}}},
		{name: "mente accent on tecnicamente", in: "técnicamente", want: []testsuite.Term{{Value: "tecnica", Owned: true}}},

		// ── accent fold (capital + multibyte force alloc) ─────────────────────
		{name: "capital folded", in: "Las", want: []testsuite.Term{{Value: "las", Owned: true}}},
		{name: "accent a acute folded", in: "árbol", want: []testsuite.Term{{Value: "arbol", Owned: true}}},
		{name: "accent o acute folded", in: "corazón", want: []testsuite.Term{{Value: "corazon", Owned: true}}},
		{name: "accent u acute folded", in: "según", want: []testsuite.Term{{Value: "segun", Owned: true}}},
		{name: "accent i acute folded", in: "así", want: []testsuite.Term{{Value: "asi", Owned: true}}},
		{name: "diaeresis u folded", in: "pingüino", want: []testsuite.Term{{Value: "pinguino", Owned: true}}},
		{name: "accent folded forces alloc", in: "canción", want: []testsuite.Term{{Value: "cancion", Owned: true}}},
		{name: "capital with accent Mexico", in: "México", want: []testsuite.Term{{Value: "mexico", Owned: true}}},
		{name: "accent u in publico", in: "público", want: []testsuite.Term{{Value: "publico", Owned: true}}},
		{name: "accent u in musica", in: "música", want: []testsuite.Term{{Value: "musica", Owned: true}}},
		{name: "accent o in economico", in: "económico", want: []testsuite.Term{{Value: "economico", Owned: true}}},
		{name: "accent o in historico", in: "histórico", want: []testsuite.Term{{Value: "historico", Owned: true}}},
		{name: "accent o in comunicacion", in: "comunicación", want: []testsuite.Term{{Value: "comunicacion", Owned: true}}},
		{name: "accent i in interventoria", in: "interventoría", want: []testsuite.Term{{Value: "interventoria", Owned: true}}},
		// accent + plural: both fold and strip apply
		{name: "accent fold plus plural s", in: "compañías", want: []testsuite.Term{{Value: "compañia", Owned: true}}},
		// lossy cases where accent token is very short after strip
		{name: "accent lossy mas stripped to ma", in: "más", want: []testsuite.Term{{Value: "ma", Owned: true}}},
		{name: "accent lossy tes stripped to te", in: "tés", want: []testsuite.Term{{Value: "te", Owned: true}}},
		// cafe has no strip rule, just fold
		{name: "e acute folded no rule", in: "café", want: []testsuite.Term{{Value: "cafe", Owned: true}}},

		// ── ñ preservation ────────────────────────────────────────────────────
		// ñ is a distinct letter and must not be folded; but multibyte forces alloc
		{name: "enye preserved on plural", in: "niños", want: []testsuite.Term{{Value: "niño", Owned: true}}},
		{name: "enye forces alloc even when unchanged", in: "español", want: []testsuite.Term{{Value: "español", Owned: true}}},
		{name: "enye preserved singular senora", in: "señora", want: []testsuite.Term{{Value: "señora", Owned: true}}},
		{name: "enye preserved with es plural senores", in: "señores", want: []testsuite.Term{{Value: "señor", Owned: true}}},
		{name: "enye preserved with es plural espanoles", in: "españoles", want: []testsuite.Term{{Value: "español", Owned: true}}},
		{name: "enye plus i acute in compania", in: "compañía", want: []testsuite.Term{{Value: "compañia", Owned: true}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsuite.AssertTerms(t, es.Tokenizer, tc.in, tc.want)
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
			fn:   es.Tokenizer,
			in:   "Los niños cantan",
			want: []testsuite.Term{
				{Value: "los", Owned: true},
				{Value: "niño", Owned: true},
				{Value: "cantan", Owned: false},
			},
		},
		{
			name: "capital fold plus s plural in sentence",
			fn:   es.Tokenizer,
			in:   "Las empresas firmaron contratos",
			want: []testsuite.Term{
				{Value: "las", Owned: true},
				{Value: "empresa", Owned: false},
				{Value: "firmaron", Owned: false},
				{Value: "contrato", Owned: false},
			},
		},
		{
			name: "capital fold and accent fold mixed in sentence",
			fn:   es.Tokenizer,
			in:   "El municipio certificó los contratos",
			want: []testsuite.Term{
				{Value: "el", Owned: true},
				{Value: "municipio", Owned: false},
				{Value: "certifico", Owned: true},
				{Value: "los", Owned: false},
				{Value: "contrato", Owned: false},
			},
		},
		{
			name: "mente adverb at sentence start",
			fn:   es.Tokenizer,
			in:   "Rápidamente se publicaron los resultados",
			want: []testsuite.Term{
				{Value: "rapida", Owned: true},
				{Value: "se", Owned: false},
				{Value: "publicaron", Owned: false},
				{Value: "los", Owned: false},
				{Value: "resultado", Owned: false},
			},
		},
		{
			name: "accent plural and long borrow in same sentence",
			fn:   es.Tokenizer,
			in:   "Los países latinoamericanos firmaron acuerdos",
			want: []testsuite.Term{
				{Value: "los", Owned: true},
				{Value: "pais", Owned: true},
				{Value: "latinoamericano", Owned: false},
				{Value: "firmaron", Owned: false},
				{Value: "acuerdo", Owned: false},
			},
		},
		{
			name: "multiple owned tokens in single sentence",
			fn:   es.Tokenizer,
			in:   "La comunicación fue técnicamente correcta",
			want: []testsuite.Term{
				{Value: "la", Owned: true},
				{Value: "comunicacion", Owned: true},
				{Value: "fue", Owned: false},
				{Value: "tecnica", Owned: true},
				{Value: "correcta", Owned: false},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testsuite.AssertTerms(t, tc.fn, tc.in, tc.want)
		})
	}
}
