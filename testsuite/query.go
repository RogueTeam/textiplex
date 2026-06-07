package testsuite

import (
	"bytes"

	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
)

// RunQuery filters then scores a query against s, returning the ranked doc
// indices (best first) alongside the populated context so assertions can read
// raw scores and the resolved bitmap.
func RunQuery(q *query.SimpleQuery, s *storage.Storage) (idxs []uint64, ctx *query.QueryContext) {
	searcher := query.New(s)
	ctx = &query.QueryContext{}
	searcher.FilterDocuments(ctx, q)
	searcher.BM25Score(ctx, q)
	idxs = searcher.ResolveScores(ctx)
	return idxs, ctx
}

// IndexOfDocument returns the internal index assigned to an external doc id after
// the alphabetical sort performed by SortAndBuildFrom.
func IndexOfDocument(s *storage.Storage, id string) (uint64, bool) {
	asBytes := []byte(id)
	for i, d := range s.DocumentsIds {
		if bytes.Equal(asBytes, d) {
			return uint64(i), true
		}
	}
	return 0, false
}

// ResolveDocumentIndexes maps a ranked slice of internal indices back to external ids.
func ResolveDocumentIndexes(s *storage.Storage, idxs []uint64) []string {
	out := make([]string, len(idxs))
	for i, idx := range idxs {
		out[i] = string(s.DocumentsIds[idx])
	}
	return out
}

// RunFieldScore builds a candidate bitmap, runs FieldScore against fieldHash,
// then resolves to a ranked slice (best first). Passing candidates == nil means
// "every document in the corpus"; a non-nil (even empty) slice restricts the
// candidate set to exactly those internal indices.
func RunFieldScore(s *storage.Storage, fieldHash uint64, candidates []uint64) (idxs []uint64, ctx *query.QueryContext) {
	searcher := query.New(s)
	ctx = &query.QueryContext{}

	if candidates == nil {
		for i := range s.DocumentsIds {
			ctx.Bitmap.Add(uint64(i))
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
