package query_test

import (
	"fmt"
	"testing"

	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
)

const (
	BM25BenchDocumentCout = 1_000
	BM25FieldLen          = 200 // tokens per doc field — "big enough" body
	BM25VocabularyCommon  = 50  // shared vocabulary spread across all docs
)

// prepareSearchCorpus builds benchDocCount docs, each with a body field of
// benchFieldLen tokens. Vocabulary is a mix:
//   - a common pool ("term-0".."term-49") sprinkled into every doc so posting
//     lists are long and intersection/scoring actually does work,
//   - a per-doc unique token ("uniq-<i>") so selective queries exist too.
//
// Construction happens entirely outside the benchmark clock.
func prepareSearchCorpus() (s *storage.Storage) {
	docs := make([]*storage.Document, 0, BM25BenchDocumentCout)

	for i := range BM25BenchDocumentCout {
		tokens := make([]*storage.TokenDefinition, 0, BM25VocabularyCommon+1)

		// Common pool: each common term appears with a frequency that varies by
		// doc, giving a realistic TF distribution to score over.
		for v := range BM25VocabularyCommon {
			freq := uint32(1 + (i+v)%5)
			tokens = append(tokens, testsuite.MakeToken(fmt.Sprintf("term-%d", v), freq))
		}
		// One unique, highly selective token per doc.
		tokens = append(tokens, testsuite.MakeToken(fmt.Sprintf("uniq-%d", i), 1))

		docs = append(docs, testsuite.MakeDoc(
			fmt.Sprintf("doc-%06d", i),
			testsuite.MakeField(fieldBody, BM25FieldLen, tokens...),
		))
	}

	s = &storage.Storage{}
	s.BuildFrom(docs...)
	return s
}

// BenchmarkSearchShould measures a 3-term OR query over the common vocabulary.
// These terms hit every document, so this is the heavy path: large unioned
// bitmap, scoring across the whole corpus, full sort.
func BenchmarkBM25SearchShould(b *testing.B) {
	s := prepareSearchCorpus()
	var searcher = query.New(s)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		q := &query.SimpleQuery{}
		q.Shoulds.Keyword([]byte("term-1"), 1.0, 0)
		q.Shoulds.Keyword([]byte("term-2"), 1.0, 0)
		q.Shoulds.Keyword([]byte("term-3"), 1.0, 0)

		ctx := &query.QueryContext{}
		searcher.FilterDocuments(ctx, q)
		searcher.BM25Score(ctx, q)
		// _ = q.BM25(ctx)
	}
}

// BenchmarkSearchMust measures a 3-term AND query. All three common terms hit
// every doc, so the intersection stays large — exercises repeated And over
// long posting lists plus full-corpus scoring.
func BenchmarkBM25SearchMust(b *testing.B) {
	s := prepareSearchCorpus()
	var searcher = query.New(s)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("term-1"), 1.0, 0)
		q.Musts.Keyword([]byte("term-2"), 1.0, 0)
		q.Musts.Keyword([]byte("term-3"), 1.0, 0)

		ctx := &query.QueryContext{}
		searcher.FilterDocuments(ctx, q)
		searcher.BM25Score(ctx, q)
		// _ = q.BM25(ctx)
	}
}

// BenchmarkSearchCombined measures a realistic mixed query: a broad Should
// over common terms, narrowed by a Must, with a MustNot exclusion.
func BenchmarkBM25SearchCombined(b *testing.B) {
	s := prepareSearchCorpus()
	var searcher = query.New(s)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte("term-1"), 1.0, 0)
		q.Shoulds.Keyword([]byte("term-2"), 2.0, 0)
		q.Shoulds.Keyword([]byte("term-3"), 1.0, 0)
		q.MustNots.Keyword([]byte("term-40"), 1.0, 0)

		ctx := &query.QueryContext{}
		searcher.FilterDocuments(ctx, q)
		searcher.BM25Score(ctx, q)
		// _ = q.BM25(ctx)
	}
}

// BenchmarkSearchSelective measures a highly selective query (one matching
// doc). This isolates per-query setup overhead from scoring cost.
func BenchmarkBM25SearchSelective(b *testing.B) {
	s := prepareSearchCorpus()
	var searcher = query.New(s)
	target := fmt.Sprintf("uniq-%d", BM25BenchDocumentCout/2)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		q := &query.SimpleQuery{}
		q.Musts.Keyword([]byte(target), 1.0, 0)

		ctx := &query.QueryContext{}
		searcher.FilterDocuments(ctx, q)
		searcher.BM25Score(ctx, q)
		// _ = q.BM25(ctx)
	}
}
