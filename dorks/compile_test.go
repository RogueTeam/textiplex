package dorks_test

import (
	"iter"
	"slices"
	"strings"
	"testing"

	"github.com/RogueTeam/textiplex/dorks"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/en"
	es "github.com/RogueTeam/textiplex/tokenizer/es"
	"github.com/stretchr/testify/assert"
	"github.com/zeebo/xxh3"
)

func verbatim(in []byte) iter.Seq[*tokenizer.Token] {
	return func(yield func(*tokenizer.Token) bool) {
		if len(in) == 0 {
			return
		}
		yield(&tokenizer.Token{Value: in})
	}
}

func BuildStorageFromDocs(docs ...*storage.Document) *storage.Storage {
	s := &storage.Storage{}
	s.SortAndBuildFrom(docs...)
	return s
}

// ── The compiler must return the query it built ───────────────────────────────

// This is the canary. Everything else assumes Compile hands back a usable
// *SimpleQuery; if it returns nil this single test localizes the failure instead
// of letting every other test panic inside FilterDocuments.
func TestCompileReturnsBuiltQuery(t *testing.T) {
	assertions := assert.New(t)

	parsed, err := dorks.Parse(strings.NewReader("contrato +medellin precio:>1000"))
	if !assertions.Nil(err) {
		return
	}

	sq := parsed.Compile(en.Tokenizer, nil)
	if !assertions.NotNil(sq, "Compile must return the SimpleQuery it built, not nil") {
		return
	}

	// The clauses must carry the parsed dorks in the right buckets.
	// "contrato" (bare keyword) and "precio:>1000" (bare field range) are both
	// unprefixed, so both land in Shoulds: one Keyword + one FieldRange.
	assertions.Len(sq.Shoulds.Keywords, 1, "bare keyword should land in Shoulds.Keywords")
	assertions.Len(sq.Shoulds.FieldRanges, 1, "bare range match should land in Shoulds.FieldRanges")
	assertions.Equal(2, sq.Shoulds.Count(), "Shoulds holds the bare keyword and the bare range")

	// "+medellin" is a Must keyword.
	assertions.Len(sq.Musts.Keywords, 1, "+term should land in Musts.Keywords")
	assertions.Equal(1, sq.Musts.Count())

	assertions.Zero(sq.MustNots.Count(), "no -term, MustNots stays empty")
}

// ── Bare keyword → Should, matches the token in any field ─────────────────────

func TestCompileBareKeyword(t *testing.T) {
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(xxh3.HashString("body"), 3, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(xxh3.HashString("body"), 3, testsuite.MakeToken("medellin", 1))),
		testsuite.MakeDoc("doc-c", testsuite.MakeField(xxh3.HashString("title"), 1, testsuite.MakeToken("contrato", 1))),
	)

	type Test struct {
		name  string
		query string
		want  []string
	}
	tests := []Test{
		{"present in one body", "medellin", []string{"doc-b"}},
		{"present across fields", "contrato", []string{"doc-a", "doc-c"}},
		{"absent term matches nothing", "inexistente", []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			assertions.Equal(want, testsuite.EnglishMatchedSet(t, tc.query, s), "matched set for %q", tc.query)
		})
	}
}

// Bare keywords are analyzed with the default tokenizer, so a query folds to
// the same token the index stored. The case-folding en.Tokenizer analyzer makes
// "Contrato"/"CONTRATO" reach the stored "contrato".
func TestCompileBareKeywordIsAnalyzed(t *testing.T) {
	assertions := assert.New(t)

	s := BuildStorageFromDocs(
		testsuite.MakeDoc("lower", testsuite.MakeField(xxh3.HashString("body"), 1, testsuite.MakeToken("contrato", 1))),
	)

	for _, q := range []string{"contrato", "Contrato", "CONTRATO"} {
		assertions.Equal([]string{"lower"}, testsuite.EnglishMatchedSet(t, q, s),
			"query %q must analyze to the stored token", q)
	}
}

// A multi-word phrase is analyzed into several terms. As a bare (Should) phrase
// each term is added independently, so a doc carrying any of them matches.
func TestCompilePhraseExpandsToTerms(t *testing.T) {
	assertions := assert.New(t)

	body := xxh3.HashString("body")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("both", testsuite.MakeField(body, 2,
			testsuite.MakeToken("obra", 1), testsuite.MakeToken("publica", 1))),
		testsuite.MakeDoc("one", testsuite.MakeField(body, 1,
			testsuite.MakeToken("publica", 1))),
		testsuite.MakeDoc("none", testsuite.MakeField(body, 1,
			testsuite.MakeToken("privada", 1))),
	)

	// The phrase compiles to two Should keywords [obras, publicas].
	sq := testsuite.CompileSpanishQuery(t, `"Obras Publicas"`)
	if assertions.NotNil(sq) {
		assertions.Len(sq.Shoulds.Keywords, 2, "phrase must expand into one Should per term")
	}

	got := testsuite.SpanishMatchedSet(t, `"Obras Publicas"`, s)
	assertions.Equal([]string{"both", "one"}, got, "any term of the phrase matches as a Should")
}

// THE critical case for the design question: Musts are analyzed too. If they
// were not, "+CORRIENDO" would look up the literal bytes, miss the stored
// (folded) token, and FilterDocuments would CLEAR the whole result set — zero
// results. Analysis makes it fold to "corriendo" and match.
func TestCompileMustIsAnalyzed(t *testing.T) {
	assertions := assert.New(t)

	body := xxh3.HashString("body")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("has", testsuite.MakeField(body, 1, testsuite.MakeToken("corriendo", 1))),
		testsuite.MakeDoc("hasnt", testsuite.MakeField(body, 1, testsuite.MakeToken("caminando", 1))),
	)

	// Upper-case Must term still finds the lower-case stored token.
	assertions.Equal([]string{"has"}, testsuite.EnglishMatchedSet(t, "+CORRIENDO", s),
		"an analyzed Must folds to the stored token instead of zeroing the result set")

	// MustNot is analyzed as well: "-Caminando" excludes the doc storing "caminando".
	assertions.Equal([]string{"has"}, testsuite.EnglishMatchedSet(t, "+corriendo -Caminando", s),
		"MustNot is analyzed and excludes the matching doc")
}

// ── Operators: +Must, -MustNot, bare Should ───────────────────────────────────

func TestCompileOperators(t *testing.T) {
	// Each token lives in its own field-less-collision body so plain keyword
	// scans stay unambiguous; ids encode which tokens a doc carries.
	body := xxh3.HashString("body")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("ab", testsuite.MakeField(body, 2,
			testsuite.MakeToken("alpha", 1), testsuite.MakeToken("beta", 1))),
		testsuite.MakeDoc("a", testsuite.MakeField(body, 1,
			testsuite.MakeToken("alpha", 1))),
		testsuite.MakeDoc("b", testsuite.MakeField(body, 1,
			testsuite.MakeToken("beta", 1))),
		testsuite.MakeDoc("none", testsuite.MakeField(body, 1,
			testsuite.MakeToken("gamma", 1))),
	)

	type Test struct {
		name  string
		query string
		want  []string
	}
	tests := []Test{
		{"should union", "alpha beta", []string{"a", "ab", "b"}},
		{"must intersection", "+alpha +beta", []string{"ab"}},
		{"must then mustnot", "+alpha -beta", []string{"a"}},
		{"should minus mustnot", "alpha -beta", []string{"a"}},
		{"absent must clears set", "+alpha +inexistente", []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			assertions.Equal(want, testsuite.EnglishMatchedSet(t, tc.query, s), "matched set for %q", tc.query)
		})
	}
}

// ── field:keyword scopes the match to one field ───────────────────────────────

func TestCompileFieldKeywordScoped(t *testing.T) {
	assertions := assert.New(t)

	// Both docs hold the token "active" but in different fields. A scoped
	// "status:active" must only reach the one whose status field carries it.
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("in-status",
			testsuite.MakeField(xxh3.HashString("status"), 1, testsuite.MakeToken("active", 1))),
		testsuite.MakeDoc("in-type",
			testsuite.MakeField(xxh3.HashString("type"), 1, testsuite.MakeToken("active", 1))),
	)

	assertions.Equal([]string{"in-status"}, testsuite.EnglishMatchedSet(t, "status:active", s),
		"scoped field keyword must ignore the same token in another field")
	assertions.Equal([]string{"in-type"}, testsuite.EnglishMatchedSet(t, "type:active", s))

	// Unscoped, the same token is reached in every field.
	assertions.Equal([]string{"in-status", "in-type"}, testsuite.EnglishMatchedSet(t, "active", s),
		"bare keyword reaches the token in any field")

	// Scoping to a field that has no such token matches nothing.
	assertions.Empty(testsuite.EnglishMatchedSet(t, "status:inactive", s))
}

// A field listed in fieldsTokenizer is analyzed with ITS tokenizer, not the
// default. Here "codigo" uses a verbatim (case-preserving) analyzer while the
// default folds case, so the index stores "AB12" verbatim and only a
// case-exact scoped query reaches it.
func TestCompileFieldTokenizerSelected(t *testing.T) {
	assertions := assert.New(t)

	codigo := xxh3.HashString("codigo")
	// Index-time: codigo is a verbatim field, so the stored token keeps its case.
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("c", testsuite.MakeField(codigo, 1, testsuite.MakeToken("AB12", 1))),
	)

	fields := map[uint64]tokenizer.Tokenizer{codigo: verbatim}

	// Field analyzer (verbatim) keeps case → exact query matches.
	assertions.Equal([]string{"c"}, testsuite.MatchedSetWith(t, "codigo:AB12", s, en.Tokenizer, fields),
		"scoped match must use the field's verbatim analyzer")

	// Same field, folded query → verbatim analyzer does NOT fold → no match.
	assertions.Empty(testsuite.MatchedSetWith(t, "codigo:ab12", s, en.Tokenizer, fields),
		"the field analyzer is verbatim, so case must matter here")

	// A field NOT in the map falls back to the default analyzer.
	otro := xxh3.HashString("otro")
	s2 := BuildStorageFromDocs(
		testsuite.MakeDoc("d", testsuite.MakeField(otro, 1, testsuite.MakeToken("rojo", 1))),
	)
	assertions.Equal([]string{"d"}, testsuite.MatchedSetWith(t, "otro:ROJO", s2, en.Tokenizer, fields),
		"unmapped field falls back to the default analyzer (which folds case)")
}

// ── Numeric / float / date exact matches use sortable encoding ────────────────

func TestCompileIntegerExact(t *testing.T) {
	assertions := assert.New(t)

	price := xxh3.HashString("price")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("p100", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(100), 1))),
		testsuite.MakeDoc("p200", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(200), 1))),
		testsuite.MakeDoc("p300", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(300), 1))),
	)

	assertions.Equal([]string{"p200"}, testsuite.EnglishMatchedSet(t, "price:200", s), "exact integer match")
	assertions.Empty(testsuite.EnglishMatchedSet(t, "price:250", s), "no doc at 250")
	// Quoted form parses to the same integer.
	assertions.Equal([]string{"p100"}, testsuite.EnglishMatchedSet(t, `price:"100"`, s), "quoted integer match")
}

func TestCompileFloatExact(t *testing.T) {
	assertions := assert.New(t)

	rate := xxh3.HashString("rate")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("r-low", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(0.5), 1))),
		testsuite.MakeDoc("r-mid", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(1.5), 1))),
		testsuite.MakeDoc("r-high", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(2.5), 1))),
	)

	assertions.Equal([]string{"r-mid"}, testsuite.EnglishMatchedSet(t, "rate:1.5", s), "exact float match")
	assertions.Empty(testsuite.EnglishMatchedSet(t, "rate:1.6", s))
}

func TestCompileDateExact(t *testing.T) {
	assertions := assert.New(t)

	created := xxh3.HashString("created_at")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("d2019", testsuite.MakeField(created, 1, testsuite.MakeToken(testsuite.SortableDate(t, "2019-06-15"), 1))),
		testsuite.MakeDoc("d2020", testsuite.MakeField(created, 1, testsuite.MakeToken(testsuite.SortableDate(t, "2020-01-31"), 1))),
	)

	assertions.Equal([]string{"d2020"}, testsuite.EnglishMatchedSet(t, "created_at:2020-01-31", s), "exact date match")
	assertions.Equal([]string{"d2020"}, testsuite.EnglishMatchedSet(t, `created_at:"2020-01-31"`, s), "quoted date match")
	assertions.Empty(testsuite.EnglishMatchedSet(t, "created_at:2021-01-01", s))
}

// ── Range matches: inclusive boundaries, ">" == ">=", "<" == "<=" ─────────────

func TestCompileIntegerRanges(t *testing.T) {
	price := xxh3.HashString("price")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("p50", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(50), 1))),
		testsuite.MakeDoc("p100", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(100), 1))),
		testsuite.MakeDoc("p150", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(150), 1))),
		testsuite.MakeDoc("p200", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(200), 1))),
	)

	type Test struct {
		name  string
		query string
		want  []string
	}
	// NOTE: the boundary is inclusive and ">" is lowered to the same range as
	// ">=", so "price:>100" includes the doc at exactly 100. Same for "<"/"<=".
	tests := []Test{
		{"greater than (inclusive)", "price:>100", []string{"p100", "p150", "p200"}},
		{"greater or equal", "price:>=100", []string{"p100", "p150", "p200"}},
		{"less than (inclusive)", "price:<150", []string{"p100", "p150", "p50"}},
		{"less or equal", "price:<=150", []string{"p100", "p150", "p50"}},
		{"above everything", "price:>1000", []string{}},
		{"below everything", "price:<10", []string{}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)
			want := slices.Clone(tc.want)
			slices.Sort(want)
			assertions.Equal(want, testsuite.EnglishMatchedSet(t, tc.query, s), "matched set for %q", tc.query)
		})
	}
}

func TestCompileFloatRange(t *testing.T) {
	assertions := assert.New(t)

	rate := xxh3.HashString("rate")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("neg", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(-3.5), 1))),
		testsuite.MakeDoc("zero", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(0.0), 1))),
		testsuite.MakeDoc("pos", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(2.25), 1))),
	)

	// Sortable float encoding must keep negative < zero < positive ordering.
	assertions.Equal([]string{"pos", "zero"}, testsuite.EnglishMatchedSet(t, "rate:>=0.0", s),
		"range over floats must respect signed numeric order")
	assertions.Equal([]string{"neg", "zero"}, testsuite.EnglishMatchedSet(t, "rate:<=0.0", s))
}

// ── A ";boost" suffix scales the field match's BM25 contribution ──────────────

func TestCompileBoostScalesScore(t *testing.T) {
	assertions := assert.New(t)

	status := xxh3.HashString("status")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("only", testsuite.MakeField(status, 1, testsuite.MakeToken("active", 1))),
	)

	plain := testsuite.CompileEnglishQuery(t, "status:active")
	boosted := testsuite.CompileEnglishQuery(t, "status:active;3.0")
	if plain == nil || boosted == nil {
		return
	}

	_, plainCtx := testsuite.RunQuery(plain, s)
	_, boostedCtx := testsuite.RunQuery(boosted, s)

	idx, ok := testsuite.IndexOfDocument(s, "only")
	if !assertions.True(ok) {
		return
	}

	base := plainCtx.Scores[idx]
	assertions.Greater(base, 0.0, "unboosted match must score positive")
	// Boost multiplies the per-term contribution linearly (Boost * ScoreTermBM25).
	assertions.InDelta(3.0*base, boostedCtx.Scores[idx], 1e-9,
		"boost of 3.0 must triple the score of the field match")
}

// ── Realistic analyzer: the index stores stems, the query is the full word ────

// This is the scenario behind the design question. With a real stemmer the
// stored token is NOT the raw word ("corriendo" → its Spanish stem). Because
// Compile applies the same analyzer, both a bare Should and a +Must reach it —
// the stored token is computed from the very same tokenizer so the test can
// never drift from the stemmer's actual output.
func TestCompileWithRealSpanishStemmer(t *testing.T) {
	assertions := assert.New(t)

	stem := func(word string) string {
		for tk := range es.Tokenizer([]byte(word)) {
			return string(tk.Value)
		}
		return word
	}

	body := xxh3.HashString("body")
	s := BuildStorageFromDocs(
		testsuite.MakeDoc("run", testsuite.MakeField(body, 1, testsuite.MakeToken(stem("corriendo"), 1))),
		testsuite.MakeDoc("walk", testsuite.MakeField(body, 1, testsuite.MakeToken(stem("caminando"), 1))),
	)

	assertions.Equal([]string{"run"}, testsuite.MatchedSetWith(t, "corriendo", s, es.Tokenizer, nil),
		"bare Should analyzed by the Spanish tokenizer matches the stored stem")
	assertions.Equal([]string{"run"}, testsuite.MatchedSetWith(t, "+Corriendo", s, es.Tokenizer, nil),
		"a Must analyzed by the Spanish tokenizer still matches the stored stem")
}

// ── Compound queries: Must / Should-range / MustNot interaction ───────────────
//
// IMPORTANT BEHAVIOUR (pinned, possibly surprising):
//
// FilterDocuments defines the candidate set from the Musts when any Must exists;
// Shoulds then only influence SCORING, not membership. The compiler routes an
// UNPREFIXED "field:>value" into Shoulds. So once a "+" term is present, an
// unprefixed range no longer filters — it only re-ranks. To make a range act as
// a filter alongside other constraints it has to participate in the set: either
// it is the sole (Should-defined) constraint, or it carries a "+".
//
// These two sub-tests pin both halves of that contract so a future change to
// FilterDocuments shows up here loudly.
func TestCompileCompoundQuery(t *testing.T) {
	body := xxh3.HashString("body")
	estado := xxh3.HashString("estado")
	valor := xxh3.HashString("valor")

	// Three contracts. "too-cheap" is below the value threshold; "annulled" is
	// estado:anulado.
	build := func() *storage.Storage {
		return BuildStorageFromDocs(
			testsuite.MakeDoc("good",
				testsuite.MakeField(body, 1, testsuite.MakeToken("contrato", 1)),
				testsuite.MakeField(estado, 1, testsuite.MakeToken("activo", 1)),
				testsuite.MakeField(valor, 1, testsuite.MakeToken(testsuite.SortableInt64(2_000_000), 1))),
			testsuite.MakeDoc("too-cheap",
				testsuite.MakeField(body, 1, testsuite.MakeToken("contrato", 1)),
				testsuite.MakeField(estado, 1, testsuite.MakeToken("activo", 1)),
				testsuite.MakeField(valor, 1, testsuite.MakeToken(testsuite.SortableInt64(500_000), 1))),
			testsuite.MakeDoc("annulled",
				testsuite.MakeField(body, 1, testsuite.MakeToken("contrato", 1)),
				testsuite.MakeField(estado, 1, testsuite.MakeToken("anulado", 1)),
				testsuite.MakeField(valor, 1, testsuite.MakeToken(testsuite.SortableInt64(2_000_000), 1))),
		)
	}

	t.Run("unprefixed range under a Must only scores, does not filter", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()

		// +contrato defines the set; -estado:anulado removes the annulled doc.
		// valor:>=1000000 is a Should range, so "too-cheap" is NOT filtered out
		// despite being below the threshold.
		got := testsuite.SpanishMatchedSet(t, "+contrato valor:>=1000000 -estado:anulado", s)
		assertions.Equal([]string{"good", "too-cheap"}, got,
			"with a Must present, the unprefixed range does not constrain membership")
	})

	t.Run("prefixed range participates in the Must intersection", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()

		// +valor:>=1000000 now joins the Must set. Only one distinct in-range
		// value (2_000_000) exists, so the Must intersection is well defined:
		// {good, annulled} ∩ {contrato docs} then minus anulado → {good}.
		got := testsuite.SpanishMatchedSet(t, "+contrato +valor:>=1000000 -estado:anulado", s)
		assertions.Equal([]string{"good"}, got,
			"a +range filters the candidate set alongside the +keyword")
	})
}
