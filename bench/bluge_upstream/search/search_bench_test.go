package tokenizers_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/blugelabs/bluge"
	"github.com/blugelabs/bluge/analysis"
	"github.com/blugelabs/bluge/analysis/tokenizer"
	"github.com/pluto-org-co/bluge/testsuite"
)

// This benchmark mirrors the textiplex query/simple_bench_test.go corpus and
// queries so the two engines can be compared on the same search workload.
//
// textiplex corpus (prepareSearchCorpus):
//   - 1000 docs
//   - each doc has one body field of 200 tokens
//   - vocabulary: common pool "term-0".."term-49" present in every doc with a
//     per-doc varying frequency (1 + (i+v)%5), plus one unique "uniq-<i>" token
//
// Token model — IMPORTANT:
// textiplex treats "term-N" and "uniq-N" as ATOMIC tokens (one posting list and
// one explicit frequency per token). Bluge's DEFAULT text analyzer (unicode
// tokenizer) splits on '-', so "term-1" would be indexed as TWO tokens
// ["term","1"]. That both (a) breaks the search and (b) collapses every common
// term's "term" prefix into a single giant posting list, destroying the per-term
// IDF the BM25 comparison depends on.
//
// To stay faithful to textiplex we index the body with a WHITESPACE tokenizer so
// each "term-N"/"uniq-N" remains a single atomic token, and we search with
// TermQuery (exact term, no re-analysis) instead of MatchPhraseQuery (which, with
// no analyzer set, single-tokens the input and would never match the split index).

const (
	benchDocCount    = 1_000
	benchVocabCommon = 50
	fieldBody        = "body"
)

// bodyAnalyzer keeps "term-N"/"uniq-N" as single tokens, splitting only on
// whitespace. The corpus is already lowercase, so no lowercase filter is needed;
// add analysis/token/lowercase.NewLowerCaseFilter() to .TokenFilters if that
// ever changes.
var bodyAnalyzer = &analysis.Analyzer{
	Tokenizer: tokenizer.NewWhitespaceTokenizer(),
}

// buildBodyText produces the materialized token stream for doc i, matching the
// frequency distribution used by textiplex's prepareSearchCorpus.
func buildBodyText(i int) string {
	var sb strings.Builder
	for v := range benchVocabCommon {
		freq := 1 + (i+v)%5
		for range freq {
			fmt.Fprintf(&sb, "term-%d ", v)
		}
	}
	fmt.Fprintf(&sb, "uniq-%d", i)
	return sb.String()
}

// buildSearchIndex builds the in-memory Bluge index outside the benchmark clock
// and returns a reader to run queries against.
func buildSearchIndex(tb testing.TB) *bluge.Reader {
	tb.Helper()

	config := bluge.DefaultConfig(testsuite.TemporaryDirectory(tb))
	writer, err := bluge.OpenWriter(config)
	if err != nil {
		tb.Fatalf("open writer: %v", err)
	}

	batch := bluge.NewBatch()
	for i := range benchDocCount {
		doc := bluge.NewDocument(fmt.Sprintf("doc-%06d", i))
		doc.AddField(bluge.NewTextField(fieldBody, buildBodyText(i)).
			WithAnalyzer(bodyAnalyzer))
		batch.Insert(doc)
	}
	if err := writer.Batch(batch); err != nil {
		tb.Fatalf("batch update: %v", err)
	}

	reader, err := writer.Reader()
	if err != nil {
		tb.Fatalf("reader: %v", err)
	}
	tb.Cleanup(func() {
		reader.Close()
		writer.Close()
	})
	return reader
}

// drainAll runs the search and consumes all hits so the cost of iterating the
// result set is included, equivalent to textiplex resolving the ranked idx slice.
func drainAll(b *testing.B, reader *bluge.Reader, q bluge.Query) {
	req := bluge.NewAllMatches(q).WithStandardAggregations()
	dmi, err := reader.Search(b.Context(), req)
	if err != nil {
		b.Fatalf("search: %v", err)
	}
	var count int
	match, err := dmi.Next()
	for match != nil && err == nil {
		count++
		match, err = dmi.Next()
	}
	if err != nil {
		b.Fatalf("iterate matches: %v", err)
	}
	_ = count
}

// BenchmarkBlugeSearchShould — 3-term OR over the common vocabulary.
// Equivalent to textiplex BenchmarkSearchShould. Matches all 1000 docs.
func BenchmarkBlugeSearchShould(b *testing.B) {
	reader := buildSearchIndex(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := bluge.NewBooleanQuery()
		for _, term := range []string{"term-1", "term-2", "term-3"} {
			q.AddShould(bluge.NewTermQuery(term).SetField(fieldBody))
		}
		drainAll(b, reader, q)
	}
}

// BenchmarkBlugeSearchMust — 3-term AND over the common vocabulary.
// Equivalent to textiplex BenchmarkSearchMust. Matches all 1000 docs.
func BenchmarkBlugeSearchMust(b *testing.B) {
	reader := buildSearchIndex(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := bluge.NewBooleanQuery()
		for _, term := range []string{"term-1", "term-2", "term-3"} {
			q.AddMust(bluge.NewTermQuery(term).SetField(fieldBody))
		}
		drainAll(b, reader, q)
	}
}

// BenchmarkBlugeSearchCombined — Must + boosted Shoulds + MustNot.
// Equivalent to textiplex BenchmarkSearchCombined.
//
// NOTE: "term-40" is in the common pool, so it is present in EVERY doc; the
// MustNot therefore excludes the entire result set (0 hits). This mirrors the
// textiplex corpus exactly and still exercises the MustNot path, but the Shoulds
// never get scored. To benchmark a non-empty scored result instead, exclude a
// token that is not universal, e.g. a "uniq-<i>" term.
func BenchmarkBlugeSearchCombined(b *testing.B) {
	reader := buildSearchIndex(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := bluge.NewBooleanQuery()
		q.AddMust(bluge.NewTermQuery("term-1").SetField(fieldBody))
		q.AddShould(bluge.NewTermQuery("term-2").SetField(fieldBody).SetBoost(2.0))
		q.AddShould(bluge.NewTermQuery("term-3").SetField(fieldBody))
		q.AddMustNot(bluge.NewTermQuery("term-40").SetField(fieldBody))
		drainAll(b, reader, q)
	}
}

// BenchmarkBlugeSearchSelective — single highly selective term (one matching doc).
// Equivalent to textiplex BenchmarkSearchSelective. Matches exactly 1 doc.
func BenchmarkBlugeSearchSelective(b *testing.B) {
	reader := buildSearchIndex(b)
	target := fmt.Sprintf("uniq-%d", benchDocCount/2)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := bluge.NewBooleanQuery()
		q.AddMust(bluge.NewTermQuery(target).SetField(fieldBody))
		drainAll(b, reader, q)
	}
}
