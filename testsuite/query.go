package testsuite

import (
	"bytes"
	"strings"

	"github.com/RogueTeam/textiplex/levenshtein"
	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
)

// RunQuery filters then scores a query against s, returning the ranked doc
// indices (best first) alongside the populated context so assertions can read
// raw scores and the resolved bitmap.
func RunQuery(q *query.SimpleQuery, s *storage.Storage) (idxs []uint32, ctx *query.QueryContext) {
	searcher := query.New(s)
	searcher.LevenshteinM = levenshtein.DefaultM
	searcher.LevenshteinMaxK = levenshtein.DefaultK
	ctx = &query.QueryContext{}
	searcher.FilterDocuments(ctx, q)
	searcher.BM25Score(ctx, q)
	idxs = searcher.ResolveScores(ctx)
	return idxs, ctx
}

// IndexOfDocument returns the internal index assigned to an external doc id after
// the alphabetical sort performed by BuildFrom.
func IndexOfDocument(s *storage.Storage, id string) (uint32, bool) {
	asBytes := []byte(id)
	for i := range s.DocumentsIds {
		d := &s.DocumentsIds[i]
		if bytes.Equal(asBytes, d.Value.Bytes()) {
			return uint32(i), true
		}
	}
	return 0, false
}

// ResolveDocumentIndexes maps a ranked slice of internal indices back to external ids.
func ResolveDocumentIndexes(s *storage.Storage, idxs []uint32) []string {
	out := make([]string, len(idxs))
	for i, idx := range idxs {
		out[i] = strings.Clone(s.DocumentsIds[idx].Value.UnsafeString())
	}
	return out
}

// RunFieldScore builds a candidate bitmap, runs FieldScore against fieldHash,
// then resolves to a ranked slice (best first). Passing candidates == nil means
// "every document in the corpus"; a non-nil (even empty) slice restricts the
// candidate set to exactly those internal indices.
func RunFieldScore(s *storage.Storage, fieldHash uint64, candidates []uint32) (idxs []uint32, ctx *query.QueryContext) {
	searcher := query.New(s)
	ctx = &query.QueryContext{}

	if candidates == nil {
		for i := range s.DocumentsIds {
			ctx.Bitmap.Add(uint32(i))
		}
	} else {
		for _, c := range candidates {
			ctx.Bitmap.Add(c)
		}
	}

	searcher.FieldScore(ctx, fieldHash)
	idxs = searcher.ResolveScores(ctx)
	return idxs, ctx
}
