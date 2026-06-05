package tokenizers_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pluto-org-co/bluge"
	"github.com/pluto-org-co/bluge/documents"
	"github.com/pluto-org-co/bluge/index"
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
// Bluge has no direct "token bag with explicit frequency" primitive, so the
// faithful equivalent is a TEXT field whose content is the materialized token
// stream (each term repeated `freq` times, space-joined). Bluge's analyzer then
// produces exactly the same per-term frequencies and field length, which is what
// BM25 scores over.

const (
	benchDocCount    = 1_000
	benchVocabCommon = 50
	fieldBody        = "body"
)

// buildBodyText produces the materialized token stream for doc i, matching the
// frequency distribution used by textiplex's prepareSearchCorpus.
func buildBodyText(i int) string {
	var sb strings.Builder
	for v := range benchVocabCommon {
		freq := 1 + (i+v)%5
		for range freq {
			sb.WriteString(fmt.Sprintf("term-%d ", v))
		}
	}
	sb.WriteString(fmt.Sprintf("uniq-%d", i))
	return sb.String()
}

// buildSearchIndex builds the in-memory Bluge index outside the benchmark clock
// and returns a reader to run queries against.
func buildSearchIndex(tb testing.TB) *bluge.Reader {
	tb.Helper()

	config := bluge.InMemoryOnlyConfig()
	writer, err := bluge.OpenWriter(config)
	if err != nil {
		tb.Fatalf("open writer: %v", err)
	}

	batch := index.NewBatch()
	for i := range benchDocCount {
		doc := documents.NewDocument(fmt.Sprintf("doc-%06d", i))
		doc.AddField(documents.NewTextField(fieldBody, buildBodyText(i)))
		batch.Update(doc.ID(), doc)
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

// drainTopN runs the search and consumes all hits so the cost of iterating the
// result set is included, equivalent to textiplex resolving the ranked idx slice.
func drainTopN(b *testing.B, reader *bluge.Reader, q bluge.Query) {
	req := bluge.NewTopNSearch(benchDocCount, q).WithStandardAggregations()
	dmi, err := reader.Search(b.Context(), req)
	if err != nil {
		b.Fatalf("search: %v", err)
	}
	match, err := dmi.Next()
	for match != nil && err == nil {
		match, err = dmi.Next()
	}
	if err != nil {
		b.Fatalf("iterate matches: %v", err)
	}
}

// BenchmarkBlugeSearchShould — 3-term OR over the common vocabulary.
// Equivalent to textiplex BenchmarkSearchShould.
func BenchmarkBlugeSearchShould(b *testing.B) {
	reader := buildSearchIndex(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := bluge.NewBooleanQuery()
		for _, term := range []string{"term-1", "term-2", "term-3"} {
			q.AddShould(bluge.NewTermQuery(term).SetField(fieldBody))
		}
		drainTopN(b, reader, q)
	}
}

// BenchmarkBlugeSearchMust — 3-term AND over the common vocabulary.
// Equivalent to textiplex BenchmarkSearchMust.
func BenchmarkBlugeSearchMust(b *testing.B) {
	reader := buildSearchIndex(b)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := bluge.NewBooleanQuery()
		for _, term := range []string{"term-1", "term-2", "term-3"} {
			q.AddMust(bluge.NewTermQuery(term).SetField(fieldBody))
		}
		drainTopN(b, reader, q)
	}
}

// BenchmarkBlugeSearchCombined — Must + boosted Shoulds + MustNot.
// Equivalent to textiplex BenchmarkSearchCombined.
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
		drainTopN(b, reader, q)
	}
}

// BenchmarkBlugeSearchSelective — single highly selective term (one matching doc).
// Equivalent to textiplex BenchmarkSearchSelective.
func BenchmarkBlugeSearchSelective(b *testing.B) {
	reader := buildSearchIndex(b)
	target := fmt.Sprintf("uniq-%d", benchDocCount/2)

	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		q := bluge.NewBooleanQuery()
		q.AddMust(bluge.NewTermQuery(target).SetField(fieldBody))
		drainTopN(b, reader, q)
	}
}
