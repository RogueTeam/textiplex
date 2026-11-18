package textiplex

import (
	"errors"
	"fmt"
	"os"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/storage"
)

var DefaultMaxWorkers = runtime.NumCPU()

const (
	// Segment files in Writer.Directory. Every other file is ignored.
	SegmentExtension = ".seg"
	// A merge in progress records its inputs in "<output>.seg.parents" so an
	// interrupted merge can be finished on the next Merge.
	parentsExtension = ".parents"
	// Attempts at finding a free segment name before giving up.
	maxNameAttempts = 1024
)

// Writer abstract the entire logic needed to write
// a multi-segment index to then merge into a single unit segment
//
// Only one process may write to a Directory at a time, and Merge must not run
// while Batch calls are in flight.
type Writer struct {
	// Maximum number of concurrent workers for merge
	// Zero or negative means DefaultMaxWorkers.
	MaxWorkers         int
	TemporaryDirectory string
	Directory          string
	// When set, Merge runs storage.Verify on both inputs of every merge before
	// merging them, so a damaged segment is reported instead of being merged
	// (and its damage carried into a fresh, valid-looking checksum) and then
	// deleted. Costs one extra full read of every input.
	VerifySegments bool
	// Last segment number used. Segment names continue after the highest
	// existing segment in Directory, so a new Writer never reuses a name.
	SegmentCounter atomic.Int64

	counterOnce sync.Once
	counterErr  error
}

// segmentNumber parses "%016X.seg" names.
func segmentNumber(name string) (n int64, ok bool) {
	base, found := strings.CutSuffix(name, SegmentExtension)
	if !found || len(base) != 16 {
		return 0, false
	}
	v, err := strconv.ParseUint(base, 16, 63)
	if err != nil {
		return 0, false
	}
	return int64(v), true
}

func (w *Writer) initCounter() (err error) {
	entries, err := os.ReadDir(w.Directory)
	if err != nil {
		return fmt.Errorf("failed to list directory: %w", err)
	}
	for _, entry := range entries {
		name := strings.TrimSuffix(entry.Name(), parentsExtension)
		if n, ok := segmentNumber(name); ok {
			for {
				current := w.SegmentCounter.Load()
				if n <= current || w.SegmentCounter.CompareAndSwap(current, n) {
					break
				}
			}
		}
	}
	return nil
}

func (w *Writer) nextSegmentName() (name string, err error) {
	w.counterOnce.Do(func() { w.counterErr = w.initCounter() })
	if w.counterErr != nil {
		return "", w.counterErr
	}
	return path.Join(w.Directory, fmt.Sprintf("%016X%s", w.SegmentCounter.Add(1), SegmentExtension)), nil
}

// isSegmentTemporaryFile reports whether name is the temporary file of a
// segment write (Batch or Merge) that never completed.
func isSegmentTemporaryFile(name string) bool {
	base, ok := strings.CutPrefix(name, ".")
	if !ok || len(base) < 16+len(SegmentExtension) {
		return false
	}
	final := base[:16+len(SegmentExtension)]
	if _, ok := segmentNumber(final); !ok {
		return false
	}
	return storage.IsTemporaryFile(name, final)
}

// segments lists the segment files in Directory, sorted by name.
func (w *Writer) segments() (names []string, err error) {
	return listSegments(w.Directory)
}

func listSegments(dir string) (names []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("failed to list directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if !entry.Type().IsRegular() || strings.HasPrefix(name, ".") || !strings.HasSuffix(name, SegmentExtension) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}

// Thread safe handler for indexing data into textiplex
func (w *Writer) Batch(batch *fields.Batch) (err error) {
	var stg storage.Storage
	if err = stg.BuildFrom(batch.Documents...); err != nil {
		return fmt.Errorf("failed to build segment: %w", err)
	}

	for range maxNameAttempts {
		filename, err := w.nextSegmentName()
		if err != nil {
			return err
		}
		err = stg.SaveToNew(filename)
		if errors.Is(err, os.ErrExist) {
			continue // taken by another writer since the counter was read
		}
		if err != nil {
			return fmt.Errorf("failed to save segment into file: %s: %w", filename, err)
		}
		return nil
	}
	return fmt.Errorf("failed to find a free segment name in %s", w.Directory)
}

// Should be called after all insertions happened
// Or when no insertions are happening in the background
//
// Merge first finishes any merge interrupted by a crash, then merges segments
// pairwise until at most one remains. Each merge writes a "<output>.parents"
// record, commits its output durably, then deletes its inputs and the record,
// so a crash at any point loses no documents and duplicates none.
func (w *Writer) Merge() (err error) {
	if err = w.recoverInterruptedMerges(); err != nil {
		return fmt.Errorf("failed to finish interrupted merges: %w", err)
	}

	numWorkers := w.MaxWorkers
	if numWorkers <= 0 {
		numWorkers = DefaultMaxWorkers
	}
	numWorkers = max(1, numWorkers)
	var workers = make(chan struct{}, numWorkers)
	for range numWorkers {
		workers <- struct{}{}
	}

	for {
		segments, err := w.segments()
		if err != nil {
			return err
		}

		// Zero or one segment: nothing to merge
		if len(segments) <= 1 {
			return nil
		}

		var allErrors []error
		var errorsCh = make(chan error, (len(segments)/2)+1)

		var wg sync.WaitGroup
		for pair := range slices.Chunk(segments, 2) {
			if len(pair) != 2 {
				continue
			}
			<-workers
			wg.Go(func() {
				defer func() { workers <- struct{}{} }()
				if err := w.mergePair(pair[0], pair[1]); err != nil {
					errorsCh <- err
				}
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

func (w *Writer) mergePair(nameA, nameB string) (err error) {
	storageAFilename := path.Join(w.Directory, nameA)
	storageBFilename := path.Join(w.Directory, nameB)

	var storageA, storageB storage.Storage

	err = storageA.Load(storageAFilename)
	if err != nil {
		return fmt.Errorf("failed to load storage A: %s: %w", storageAFilename, err)
	}
	defer storageA.Close()

	err = storageB.Load(storageBFilename)
	if err != nil {
		return fmt.Errorf("failed to load storage B: %s: %w", storageBFilename, err)
	}
	defer storageB.Close()

	if w.VerifySegments {
		if err = storageA.Verify(); err != nil {
			return fmt.Errorf("storage A: %s: %w", storageAFilename, err)
		}
		if err = storageB.Verify(); err != nil {
			return fmt.Errorf("storage B: %s: %w", storageBFilename, err)
		}
	}

	var merger = storage.Merger{TempDir: w.TemporaryDirectory, NoReplace: true}
	for range maxNameAttempts {
		output, err := w.nextSegmentName()
		if err != nil {
			return err
		}
		parents := output + parentsExtension

		// The record must be durable before the output can appear.
		err = writeParents(parents, nameA, nameB)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to record merge inputs: %w", err)
		}

		err = merger.Merge(output, &storageA, &storageB)
		if err != nil {
			if _, statErr := os.Lstat(output); statErr == nil && !errors.Is(err, os.ErrExist) {
				// The output exists although Merge failed (it could not undo a
				// rename). Keep the record: the next Merge deletes the inputs,
				// which the output already contains, instead of duplicating them.
				return fmt.Errorf("failed to merge storages (output %s kept for recovery): %w", output, err)
			}
			os.Remove(parents)
			if errors.Is(err, os.ErrExist) {
				continue
			}
			return fmt.Errorf("failed to merge storages: %w", err)
		}

		// Output is committed: the inputs are now redundant.
		storageA.Close()
		storageB.Close()
		return finishMerge(w.Directory, parents, []string{nameA, nameB})
	}
	return fmt.Errorf("failed to find a free segment name in %s", w.Directory)
}

func writeParents(name string, inputs ...string) (err error) {
	file, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = file.WriteString(strings.Join(inputs, "\n") + "\n")
	if err == nil {
		err = file.Sync()
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = storage.SyncDir(path.Dir(name))
	}
	if err != nil {
		os.Remove(name)
	}
	return err
}

// finishMerge deletes the inputs of a committed merge, then its record.
func finishMerge(dir, parents string, inputs []string) (err error) {
	for _, input := range inputs {
		err = os.Remove(path.Join(dir, input))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			// Keep the record so the next Merge retries the deletion.
			return fmt.Errorf("failed to remove merged segment %s: %w", input, err)
		}
	}
	if err = storage.SyncDir(dir); err != nil {
		return err
	}
	if err = os.Remove(parents); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("failed to remove merge record: %w", err)
	}
	return storage.SyncDir(dir)
}

// recoverInterruptedMerges completes or discards merges a crash interrupted.
// If a merge's output was committed its inputs are deleted (they are already
// contained in the output); otherwise the inputs are still authoritative and
// only the record is removed. Temporary segment files that a crash left
// behind are deleted too.
func (w *Writer) recoverInterruptedMerges() (err error) {
	entries, err := os.ReadDir(w.Directory)
	if err != nil {
		return fmt.Errorf("failed to list directory: %w", err)
	}
	for _, entry := range entries {
		if isSegmentTemporaryFile(entry.Name()) && entry.Type().IsRegular() {
			err = os.Remove(path.Join(w.Directory, entry.Name()))
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("failed to remove leftover temporary file %s: %w", entry.Name(), err)
			}
		}
	}
	for _, entry := range entries {
		record, isRecord := strings.CutSuffix(entry.Name(), parentsExtension)
		if !isRecord || !entry.Type().IsRegular() {
			continue
		}
		recordPath := path.Join(w.Directory, entry.Name())

		_, statErr := os.Stat(path.Join(w.Directory, record))
		switch {
		case statErr == nil:
			contents, err := os.ReadFile(recordPath)
			if err != nil {
				return fmt.Errorf("failed to read merge record %s: %w", entry.Name(), err)
			}
			var inputs []string
			for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
				// Only plain segment names written by mergePair are trusted.
				if _, ok := segmentNumber(line); !ok || line == record {
					return fmt.Errorf("merge record %s names %q, which is not a segment", entry.Name(), line)
				}
				inputs = append(inputs, line)
			}
			if err = finishMerge(w.Directory, recordPath, inputs); err != nil {
				return err
			}
		case errors.Is(statErr, os.ErrNotExist):
			if err = os.Remove(recordPath); err != nil {
				return fmt.Errorf("failed to remove stale merge record: %w", err)
			}
		default:
			return fmt.Errorf("failed to check merge output %s: %w", record, statErr)
		}
	}
	return storage.SyncDir(w.Directory)
}
