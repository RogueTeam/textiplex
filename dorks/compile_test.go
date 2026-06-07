package dorks_test

import (
	"iter"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/RogueTeam/textiplex/dorks"
	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/stretchr/testify/assert"
	"github.com/zeebo/xxh3"
)

// ══════════════════════════════════════════════════════════════════════════════
// Query.Compile — end to end against a real storage
//
// These tests do not inspect the SimpleQuery struct field by field. Instead they
// drive the full pipeline the product uses:
//
//	dork string ──Parse──▶ *dorks.Query ──Compile──▶ *query.SimpleQuery
//	             ──FilterDocuments+BM25Score──▶ ranked doc ids
//
// and assert that the documents Compile makes the searcher match are exactly the
// ones we expect from a hand-built index. This is the strongest possible check
// that the compiler targets the right field hashes, the right clause buckets
// (Should / Must / MustNot) and the right value encodings (raw bytes for
// keywords, sortable bytes for integers / floats / dates).
//
// Behaviours pinned here that reflect the CURRENT implementation (see the notes
// inline if any of these is not what you intend):
//
//   - Compile ignores its tokenizer arguments. Keywords reach the index as their
//     raw bytes, so matching is case sensitive and unstemmed. The storage tokens
//     in these tests are therefore stored verbatim.
//   - A bare term is a Should, "+term" is a Must, "-term" is a MustNot.
//   - "field:value" scopes the match to xxh3(field); the same token in another
//     field must not match.
//   - Range operators are INCLUSIVE on the boundary, and ">" behaves like ">="
//     while "<" behaves like "<=", because the compiler lowers both to a single
//     open-ended FieldRange.
//   - A ";N" suffix multiplies the BM25 contribution of a field match by N.
// ══════════════════════════════════════════════════════════════════════════════

// fieldHash mirrors exactly what Compile does to turn a field name into the hash
// the index is keyed by. Tests build fields with this so the lookups line up.
func fieldHash(name string) uint64 { return xxh3.HashString(name) }

// verbatim is a tokenizer that emits the whole input as one unmodified token.
// Compile currently ignores the tokenizer entirely; passing a real (non-nil)
// function rather than nil keeps these tests honest if Compile later starts
// routing keywords through it, since verbatim preserves the raw-byte contract
// the storage tokens below rely on.
func verbatim(in []byte) iter.Seq[*tokenizer.Token] {
	return func(yield func(*tokenizer.Token) bool) {
		if len(in) == 0 {
			return
		}
		yield(&tokenizer.Token{Value: in})
	}
}

// compileQuery parses and compiles a dork string. It guards the documented
// contract that Compile returns the query it built: a nil result turns the
// downstream nil-pointer panic into a clear, single-line failure.
func compileQuery(t *testing.T, q string) *query.SimpleQuery {
	t.Helper()
	assertions := assert.New(t)

	parsed, err := dorks.Parse(strings.NewReader(q))
	if !assertions.Nil(err, "parse %q", q) {
		return nil
	}

	sq := parsed.Compile(verbatim, nil)
	if !assertions.NotNil(sq, "Compile(%q) returned nil — it must return the SimpleQuery it built", q) {
		return nil
	}
	return sq
}

// buildStorage sorts and indexes docs into a ready storage.
func buildStorage(docs ...*storage.Document) *storage.Storage {
	s := &storage.Storage{}
	s.SortAndBuildFrom(docs...)
	return s
}

// matchedSet compiles q, runs it against s and returns the matched external ids
// sorted, so membership can be compared regardless of ranking order.
func matchedSet(t *testing.T, q string, s *storage.Storage) []string {
	t.Helper()

	sq := compileQuery(t, q)
	if sq == nil {
		return nil
	}
	idxs, _ := testsuite.RunQuery(sq, s)
	got := testsuite.ResolveDocumentIndexes(s, idxs)
	slices.Sort(got)
	return got
}

// sortableDate encodes a DateOnly string the same way Compile does for date
// matches: parse to a time then store the sortable UnixNano.
func sortableDate(t *testing.T, s string) string {
	t.Helper()
	assertions := assert.New(t)
	tm, err := time.Parse(time.DateOnly, s)
	assertions.Nil(err, "parse date %q", s)
	return testsuite.SortableInt64(tm.UnixNano())
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

	sq := parsed.Compile(verbatim, nil)
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
	s := buildStorage(
		testsuite.MakeDoc("doc-a", testsuite.MakeField(fieldHash("body"), 3, testsuite.MakeToken("contrato", 1))),
		testsuite.MakeDoc("doc-b", testsuite.MakeField(fieldHash("body"), 3, testsuite.MakeToken("medellin", 1))),
		testsuite.MakeDoc("doc-c", testsuite.MakeField(fieldHash("title"), 1, testsuite.MakeToken("contrato", 1))),
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
			assertions.Equal(want, matchedSet(t, tc.query, s), "matched set for %q", tc.query)
		})
	}
}

// Keywords are not lowercased or stemmed by the compiler, so a query token only
// matches a storage token with identical bytes. Pins the no-tokenize behaviour.
func TestCompileKeywordIsCaseSensitive(t *testing.T) {
	assertions := assert.New(t)

	s := buildStorage(
		testsuite.MakeDoc("lower", testsuite.MakeField(fieldHash("body"), 1, testsuite.MakeToken("contrato", 1))),
	)

	assertions.Equal([]string{"lower"}, matchedSet(t, "contrato", s), "exact case must match")
	assertions.Empty(matchedSet(t, "Contrato", s), "different case must not match (compiler does not fold)")
}

// ── Operators: +Must, -MustNot, bare Should ───────────────────────────────────

func TestCompileOperators(t *testing.T) {
	// Each token lives in its own field-less-collision body so plain keyword
	// scans stay unambiguous; ids encode which tokens a doc carries.
	body := fieldHash("body")
	s := buildStorage(
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
			assertions.Equal(want, matchedSet(t, tc.query, s), "matched set for %q", tc.query)
		})
	}
}

// ── field:keyword scopes the match to one field ───────────────────────────────

func TestCompileFieldKeywordScoped(t *testing.T) {
	assertions := assert.New(t)

	// Both docs hold the token "active" but in different fields. A scoped
	// "status:active" must only reach the one whose status field carries it.
	s := buildStorage(
		testsuite.MakeDoc("in-status",
			testsuite.MakeField(fieldHash("status"), 1, testsuite.MakeToken("active", 1))),
		testsuite.MakeDoc("in-type",
			testsuite.MakeField(fieldHash("type"), 1, testsuite.MakeToken("active", 1))),
	)

	assertions.Equal([]string{"in-status"}, matchedSet(t, "status:active", s),
		"scoped field keyword must ignore the same token in another field")
	assertions.Equal([]string{"in-type"}, matchedSet(t, "type:active", s))

	// Unscoped, the same token is reached in every field.
	assertions.Equal([]string{"in-status", "in-type"}, matchedSet(t, "active", s),
		"bare keyword reaches the token in any field")

	// Scoping to a field that has no such token matches nothing.
	assertions.Empty(matchedSet(t, "status:inactive", s))
}

// ── Numeric / float / date exact matches use sortable encoding ────────────────

func TestCompileIntegerExact(t *testing.T) {
	assertions := assert.New(t)

	price := fieldHash("price")
	s := buildStorage(
		testsuite.MakeDoc("p100", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(100), 1))),
		testsuite.MakeDoc("p200", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(200), 1))),
		testsuite.MakeDoc("p300", testsuite.MakeField(price, 1, testsuite.MakeToken(testsuite.SortableInt64(300), 1))),
	)

	assertions.Equal([]string{"p200"}, matchedSet(t, "price:200", s), "exact integer match")
	assertions.Empty(matchedSet(t, "price:250", s), "no doc at 250")
	// Quoted form parses to the same integer.
	assertions.Equal([]string{"p100"}, matchedSet(t, `price:"100"`, s), "quoted integer match")
}

func TestCompileFloatExact(t *testing.T) {
	assertions := assert.New(t)

	rate := fieldHash("rate")
	s := buildStorage(
		testsuite.MakeDoc("r-low", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(0.5), 1))),
		testsuite.MakeDoc("r-mid", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(1.5), 1))),
		testsuite.MakeDoc("r-high", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(2.5), 1))),
	)

	assertions.Equal([]string{"r-mid"}, matchedSet(t, "rate:1.5", s), "exact float match")
	assertions.Empty(matchedSet(t, "rate:1.6", s))
}

func TestCompileDateExact(t *testing.T) {
	assertions := assert.New(t)

	created := fieldHash("created_at")
	s := buildStorage(
		testsuite.MakeDoc("d2019", testsuite.MakeField(created, 1, testsuite.MakeToken(sortableDate(t, "2019-06-15"), 1))),
		testsuite.MakeDoc("d2020", testsuite.MakeField(created, 1, testsuite.MakeToken(sortableDate(t, "2020-01-31"), 1))),
	)

	assertions.Equal([]string{"d2020"}, matchedSet(t, "created_at:2020-01-31", s), "exact date match")
	assertions.Equal([]string{"d2020"}, matchedSet(t, `created_at:"2020-01-31"`, s), "quoted date match")
	assertions.Empty(matchedSet(t, "created_at:2021-01-01", s))
}

// ── Range matches: inclusive boundaries, ">" == ">=", "<" == "<=" ─────────────

func TestCompileIntegerRanges(t *testing.T) {
	price := fieldHash("price")
	s := buildStorage(
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
			assertions.Equal(want, matchedSet(t, tc.query, s), "matched set for %q", tc.query)
		})
	}
}

func TestCompileFloatRange(t *testing.T) {
	assertions := assert.New(t)

	rate := fieldHash("rate")
	s := buildStorage(
		testsuite.MakeDoc("neg", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(-3.5), 1))),
		testsuite.MakeDoc("zero", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(0.0), 1))),
		testsuite.MakeDoc("pos", testsuite.MakeField(rate, 1, testsuite.MakeToken(testsuite.SortableFloat64(2.25), 1))),
	)

	// Sortable float encoding must keep negative < zero < positive ordering.
	assertions.Equal([]string{"pos", "zero"}, matchedSet(t, "rate:>=0.0", s),
		"range over floats must respect signed numeric order")
	assertions.Equal([]string{"neg", "zero"}, matchedSet(t, "rate:<=0.0", s))
}

// ── A ";boost" suffix scales the field match's BM25 contribution ──────────────

func TestCompileBoostScalesScore(t *testing.T) {
	assertions := assert.New(t)

	status := fieldHash("status")
	s := buildStorage(
		testsuite.MakeDoc("only", testsuite.MakeField(status, 1, testsuite.MakeToken("active", 1))),
	)

	plain := compileQuery(t, "status:active")
	boosted := compileQuery(t, "status:active;3.0")
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
	body := fieldHash("body")
	estado := fieldHash("estado")
	valor := fieldHash("valor")

	// Three contracts. "too-cheap" is below the value threshold; "annulled" is
	// estado:anulado.
	build := func() *storage.Storage {
		return buildStorage(
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
		got := matchedSet(t, "+contrato valor:>=1000000 -estado:anulado", s)
		assertions.Equal([]string{"good", "too-cheap"}, got,
			"with a Must present, the unprefixed range does not constrain membership")
	})

	t.Run("prefixed range participates in the Must intersection", func(t *testing.T) {
		assertions := assert.New(t)
		s := build()

		// +valor:>=1000000 now joins the Must set. Only one distinct in-range
		// value (2_000_000) exists, so the Must intersection is well defined:
		// {good, annulled} ∩ {contrato docs} then minus anulado → {good}.
		got := matchedSet(t, "+contrato +valor:>=1000000 -estado:anulado", s)
		assertions.Equal([]string{"good"}, got,
			"a +range filters the candidate set alongside the +keyword")
	})
}
