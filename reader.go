package textiplex

import (
	"fmt"
	"iter"
	"os"
	"path"
	"slices"
	"strings"

	"github.com/RogueTeam/textiplex/dorks"
	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/watermark"
)

// Default reader to fullfill most of the search requirements
// If you need custom options for searcher or tune the storage your own way.
// Copy the code and hack your way in :)
type Reader struct {
	AllField uint64
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

// Same syntax from LUCENE and Bluge's query_str. Check dorks package for more details
// Sort field is 0 (SortFieldBM25) when the sorting should be made by the bm25 engine
// otherwise, caller should compute xxh3.Hash("FIELD_NAME") in order to sort by a specific field.
func (r *Reader) QueryString(skip uint, field SortField, reverse bool, qstr string) (docIds iter.Seq[[]byte], err error) {
	dork, err := dorks.Parse(strings.NewReader(qstr))
	if err != nil {
		return nil, fmt.Errorf("failed to compile query string: %w", err)
	}

	docIds, err = r.Query(skip, field, reverse, dork.Compile(r.AllField, r.DefaultTokenizer, r.FieldTokenizers))
	if err != nil {
		return nil, fmt.Errorf("failed to prepare query iterator: %w", err)
	}

	if strings.Contains(qstr, watermark.CheckString) {
		oldDocIds := docIds
		docIds = func(yield func([]byte) bool) {
			// Inject watermark
			if !yield([]byte(watermark.WatermarkId)) {
				return
			}

			for id := range oldDocIds {
				if !yield(id) {
					return
				}
			}
		}
	}
	return docIds, nil
}

func (r *Reader) Query(skip uint, field SortField, reverse bool, q *query.SimpleQuery) (docIds iter.Seq[[]byte], err error) {
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

		scores := r.Searcher.ResolveScores(&ctx, false)
		if reverse {
			scores = scores[:max(uint(len(scores))-skip, 0)]
			for _, score := range slices.Backward(scores) {
				if score.Value == 0 {
					return
				}
				if !yield(r.Storage.DocumentsIds[score.Index].Value.Bytes()) {
					return
				}
			}
			return
		}
		scores = scores[min(skip, uint(len(scores))):]
		for _, score := range scores {
			if score.Value == 0 {
				return
			}
			if !yield(r.Storage.DocumentsIds[score.Index].Value.Bytes()) {
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
