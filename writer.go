package textiplex

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/storage"
)

var DefaultMaxWorkers = runtime.NumCPU()

// Writer abstract the entire logic needed to write
// a multi-segment index to then merge into a single unit segment
type Writer struct {
	// Maximum number of concurrent workers for merge
	MaxWorkers         int
	TemporaryDirectory string
	Directory          string
	SegmentCounter     atomic.Int64
}

// Thread safe handler for indexing data into textiplex
func (w *Writer) Batch(batch *fields.Batch) (err error) {
	var stg storage.Storage
	stg.SortAndBuildFrom(batch.Documents...)

	filename := path.Join(w.Directory, fmt.Sprintf("%016X.seg", w.SegmentCounter.Add(1)))
	err = stg.SaveTo(filename)
	if err != nil {
		return fmt.Errorf("failed to save segment into file: %s: %w", filename, err)
	}

	return nil
}

// Should be called after all insertions happened
// Or when no insertions are happening in the background
func (w *Writer) Merge() (err error) {
	var allErrors []error
	numWorkers := min(max(1, w.MaxWorkers, DefaultMaxWorkers), runtime.NumCPU())
	var workers = make(chan struct{}, numWorkers)
	for range numWorkers {
		workers <- struct{}{}
	}

	for {
		dirEntries, err := os.ReadDir(w.Directory)
		if err != nil {
			return fmt.Errorf("failed to list directory: %w", err)
		}

		// One single segment, we are done
		if len(dirEntries) == 1 {
			return nil
		}

		allErrors = allErrors[:]
		var errorsCh = make(chan error, (len(dirEntries)/2)+1)

		var wg sync.WaitGroup
		for pair := range slices.Chunk(dirEntries, 2) {
			<-workers
			wg.Go(func() {
				defer func() { workers <- struct{}{} }()
				if len(pair) != 2 {
					return
				}

				storageAFilename := path.Join(w.Directory, pair[0].Name())
				storageBFilename := path.Join(w.Directory, pair[1].Name())

				var storageA, storageB storage.Storage

				err := storageA.Load(storageAFilename)
				if err != nil {
					errorsCh <- fmt.Errorf("failed to load storage A: %s: %w", storageAFilename, err)
					return
				}
				defer storageA.Close()

				err = storageB.Load(storageBFilename)
				if err != nil {
					errorsCh <- fmt.Errorf("failed to load storage B: %s: %w", storageBFilename, err)
					return
				}
				defer storageB.Close()

				var merger = storage.Merger{TempDir: w.TemporaryDirectory}
				err = merger.Merge(
					path.Join(w.Directory, fmt.Sprintf("%016X.seg", w.SegmentCounter.Add(1))),
					&storageA,
					&storageB,
				)
				if err != nil {
					errorsCh <- fmt.Errorf("failed to merge storages: %w", err)
					return
				}

				os.Remove(storageAFilename)
				os.Remove(storageBFilename)
			})
		}
		go func() {
			wg.Wait()
			close(errorsCh)
		}()

		for err := range errorsCh {
			allErrors = append(allErrors, err)
		}

		switch len(allErrors) {
		case 0:
			continue
		case 1:
			return fmt.Errorf("one error during merge iteration: %w", allErrors[0])
		default:
			return fmt.Errorf("multiple errors during merge iteration: %w", errors.Join(allErrors...))
		}
	}
}
