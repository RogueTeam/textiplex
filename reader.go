package textiplex

import (
	"bytes"
	"fmt"
	"iter"
	"path"
	"slices"
	"strings"
	"sync"

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

	// Guards Storage and Searcher: Close and Reset wait for queries that are
	// reading the mapped index.
	mu sync.RWMutex
	// Incremented by Close and Reset so a lazy query iterator notices the index
	// it scored against is gone.
	generation uint64
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

// Query scores q and returns the matching document ids, best first. The
// sequence is lazy. Each yielded id is a copy the caller may keep. If the
// reader is closed or reset while the sequence is being consumed, the
// sequence stops early.
func (r *Reader) Query(skip uint, field SortField, reverse bool, q *query.SimpleQuery) (docIds iter.Seq[[]byte], err error) {
	r.mu.RLock()
	configured := r.Searcher != nil
	r.mu.RUnlock()
	if !configured {
		return nil, fmt.Errorf("no searcher is configured, make sure to use reset to build the reader")
	}

	docIds = func(yield func([]byte) bool) {
		// Scoring reads the mapped index: hold the read lock so Close/Reset
		// cannot unmap it underneath. The scores themselves are heap memory.
		r.mu.RLock()
		if r.Searcher == nil {
			r.mu.RUnlock()
			return
		}
		generation := r.generation
		var ctx query.QueryContext
		r.Searcher.FilterDocuments(&ctx, q)
		if field == SortFieldBM25 {
			r.Searcher.BM25Score(&ctx, q)
		} else {
			r.Searcher.FieldScore(&ctx, uint64(field))
		}
		scores := r.Searcher.ResolveScores(&ctx, false)
		r.mu.RUnlock()

		var ordered iter.Seq[query.Score]
		if reverse {
			if skip >= uint(len(scores)) {
				return
			}
			kept := scores[:uint(len(scores))-skip]
			ordered = func(yield func(query.Score) bool) {
				for _, score := range slices.Backward(kept) {
					if !yield(score) {
						return
					}
				}
			}
		} else {
			ordered = slices.Values(scores[min(skip, uint(len(scores))):])
		}

		for score := range ordered {
			if score.Value == 0 {
				return
			}
			// Copy the id out of the mapping under the lock and yield the copy
			// without it, so the consumer can keep the id and may call Close or
			// Reset from inside the loop.
			r.mu.RLock()
			if r.generation != generation {
				r.mu.RUnlock()
				return
			}
			id := bytes.Clone(r.Storage.DocumentsIds[score.Index].Value.Bytes())
			r.mu.RUnlock()
			if !yield(id) {
				return
			}
		}
	}

	return docIds, nil
}

// Close releases the index. It waits for queries that are scoring or copying
// ids; iterators still being consumed stop early.
func (r *Reader) Close() (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closeLocked()
}

func (r *Reader) closeLocked() (err error) {
	r.Searcher = nil
	r.generation++
	return r.Storage.Close()
}

// Reset closes the current index and opens the single segment in dir.
// Files other than "*.seg" segments are ignored.
func (r *Reader) Reset(dir string) (err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if err = r.closeLocked(); err != nil {
		return fmt.Errorf("failed to close previous index: %w", err)
	}

	segments, err := listSegments(dir)
	if err != nil {
		return fmt.Errorf("failed to read directory: %w", err)
	}

	switch len(segments) {
	default:
		return fmt.Errorf("directory must contain only one segment, merge first all the segments prior opening a reader")
	case 0:
		return fmt.Errorf("directory is empty")
	case 1:
		err = r.Storage.Load(path.Join(dir, segments[0]))
		if err != nil {
			return fmt.Errorf("failed load storage from segment in directory: %w", err)
		}

		r.Searcher = query.New(&r.Storage)
		return nil
	}
}
