package textiplex

import (
	"fmt"
	"iter"
	"os"
	"path"
	"strings"

	"github.com/RogueTeam/textiplex/dorks"
	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tokenizer"
)

// Default reader to fullfill most of the search requirements
// If you need custom options for searcher or tune the storage your own way.
// Copy the code and hack your way in :)
type Reader struct {
	// Is caller responsability to populate this field
	// Default tokenizer used by the search engine when no field matches
	DefaultTokenizer tokenizer.Tokenizer
	// Is caller responsability to populate this field
	// Default tokenizer used by the search engine when fields matches
	FieldTokenizers map[uint64]tokenizer.Tokenizer
	// Caller should not populate the storage
	// Use Reset(dir)
	Storage storage.Storage
	// Caller should not populate the storage
	// Use Reset(dir)
	Searcher *query.Searcher
}

// Sort field is used as sort parameter
type SortField uint64

const SortFieldBM25 SortField = 0

func (r *Reader) QueryString(field SortField, qstr string) (docIds iter.Seq[[]byte], err error) {
	dork, err := dorks.Parse(strings.NewReader(qstr))
	if err != nil {
		return nil, fmt.Errorf("failed to compile query string: %w", err)
	}

	return r.Query(field, dork.Compile(r.DefaultTokenizer, r.FieldTokenizers))
}

func (r *Reader) Query(field SortField, q *query.SimpleQuery) (docIds iter.Seq[[]byte], err error) {
	if r.Searcher == nil {
		return nil, fmt.Errorf("no searcher is configured, make sure to use reset to build the reader")
	}

	docIds = func(yield func([]byte) bool) {
		var ctx query.QueryContext
		r.Searcher.FilterDocuments(&ctx, q)
		if field == SortFieldBM25 {
			r.Searcher.BM25Score(&ctx, q)
		} else {
			r.Searcher.FieldScore(&ctx, uint64(field))
		}

		for _, docIdx := range r.Searcher.ResolveScores(&ctx) {
			if !yield(r.Storage.DocumentsIds[docIdx].Value.Bytes()) {
				return
			}
		}
	}

	return docIds, nil
}

func (r *Reader) Close() (err error) {
	r.Searcher = nil
	return r.Storage.Close()
}

func (r *Reader) Reset(dir string) (err error) {
	r.Close()

	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	switch len(entries) {
	default:
		return fmt.Errorf("directory must contain only one segment, merge first all the segments prior opening a reader")
	case 0:
		return fmt.Errorf("directory is empty")
	case 1:
		err = r.Storage.Load(path.Join(dir, entries[0].Name()))
		if err != nil {
			return fmt.Errorf("failed load storage from segment in directory: %w", err)
		}

		r.Searcher = query.New(&r.Storage)
		return nil
	}
}
