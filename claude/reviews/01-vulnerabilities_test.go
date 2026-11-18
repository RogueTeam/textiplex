// Regression tests for the storage-layer vulnerabilities and data-loss paths
// found in the review. Each test reproduces the original attack or failure
// and asserts that it is now handled.
//
// Scenarios that used to kill the process (SIGSEGV, SIGBUS, fatal OOM) or that
// need process-wide resource limits re-execute the test binary as a child
// (POC_CHILD=1), so a regression fails the test instead of the whole run.
package reviews_test

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/RogueTeam/textiplex"
	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/keyword"
	"github.com/zeebo/xxh3"
)

// ---------------------------------------------------------------- helpers ---

const tagField = "tag"

func isChild() bool { return os.Getenv("POC_CHILD") == "1" }

func runChild(t *testing.T, name string, env ...string) (string, *exec.ExitError) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+name+"$", "-test.v", "-test.count=1")
	cmd.Env = append(append(os.Environ(), "POC_CHILD=1"), env...)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return string(out), nil
	}
	ee, ok := err.(*exec.ExitError)
	if !ok {
		t.Fatalf("failed to run child: %v", err)
	}
	return string(out), ee
}

// requireChildOK fails the test unless the child exited cleanly and printed
// every expected RESULT line.
func requireChildOK(t *testing.T, out string, ee *exec.ExitError, want ...string) {
	t.Helper()
	if ee != nil {
		t.Fatalf("child died (%s):\n%s", signaled(ee), out)
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Fatalf("child output lacks %q:\n%s", w, out)
		}
	}
}

func signaled(ee *exec.ExitError) string {
	if ee == nil {
		return "exit 0"
	}
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return ws.Signal().String()
	}
	return fmt.Sprintf("exit %d", ee.ExitCode())
}

func mkDoc(id string, tokens ...string) *storage.Document {
	fd := &storage.FieldDefinition{Hash: xxh3.HashString(tagField), Length: uint32(len(tokens))}
	for _, tk := range tokens {
		fd.Tokens = append(fd.Tokens, &storage.TokenDefinition{Value: []byte(tk), Frequency: 1})
	}
	return &storage.Document{Id: storage.DocumentId{Value: storage.RawValueFrom(id)}, Fields: []*storage.FieldDefinition{fd}}
}

func manyDocs(prefix string, n int, tokens ...string) (docs []*storage.Document) {
	for i := range n {
		docs = append(docs, mkDoc(fmt.Sprintf("%s-%06d", prefix, i), tokens...))
	}
	return docs
}

func mkBatch(docs ...*storage.Document) *fields.Batch {
	b := fields.NewBatch(len(docs) + 1)
	for _, d := range docs {
		b.Insert(d.Id, 0, d.Fields...)
	}
	return b
}

func saveIndex(t testing.TB, path string, docs ...*storage.Document) {
	t.Helper()
	var s storage.Storage
	if err := s.BuildFrom(docs...); err != nil {
		t.Fatalf("BuildFrom: %v", err)
	}
	if err := s.SaveTo(path); err != nil {
		t.Fatalf("SaveTo: %v", err)
	}
}

func newReader() *textiplex.Reader {
	return &textiplex.Reader{
		DefaultTokenizer: keyword.Tokenizer,
		FieldTokenizers:  map[uint64]tokenizer.Tokenizer{xxh3.HashString(tagField): keyword.Tokenizer},
	}
}

// query returns the matching doc ids.
func query(t *testing.T, r *textiplex.Reader, q string) (ids []string) {
	t.Helper()
	seq, err := r.QueryString(0, textiplex.SortFieldBM25, false, q)
	if err != nil {
		t.Fatalf("QueryString(%q): %v", q, err)
	}
	for id := range seq {
		ids = append(ids, string(id))
	}
	return ids
}

func segmentFiles(t *testing.T, dir string) (names []string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), textiplex.SegmentExtension) && !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names
}

// countDocs loads the single segment in dir, verifies it, and returns its
// document count.
func countDocs(dir string) (n int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	var segs []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), textiplex.SegmentExtension) && !strings.HasPrefix(e.Name(), ".") {
			segs = append(segs, e.Name())
		}
	}
	if len(segs) != 1 {
		return 0, fmt.Errorf("expected one segment, got %d (%v)", len(segs), segs)
	}
	var s storage.Storage
	if err := s.Load(filepath.Join(dir, segs[0])); err != nil {
		return 0, err
	}
	defer s.Close()
	if err := s.Verify(); err != nil {
		return 0, err
	}
	return len(s.DocumentsIds), nil
}

func setFileSizeLimit(t *testing.T, limit uint64) {
	// Go ignores SIGXFSZ by default, so writes past the limit fail with EFBIG,
	// which is exactly how ENOSPC / quota errors surface to the code under test.
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: limit}); err != nil {
		t.Fatalf("setrlimit: %v", err)
	}
}

// toV1 rewrites a version 2 file as the original version 1 format: version
// field 0, no footer.
func toV1(data []byte) []byte {
	out := append([]byte(nil), data[:len(data)-int(storage.FooterSize)]...)
	binary.NativeEndian.PutUint16(out[8:], storage.VersionV1)
	return out
}

// tokenOffset is the file offset of token 0 of the only field of an index of
// n documents with one token each.
func tokenOffset(n int) int {
	return int(storage.HeaderSize) + n*int(storage.DocumentIdSize) + n*int(storage.TokenFrequencyEntrySize) +
		int(storage.FieldHeaderSize) + n*int(storage.DocumentLengthEntrySize)
}

// ------------------------------------------------------- 1. data loss -------

// Was: a new Writer reused segment names from 0 and os.Create truncated the
// existing segment, silently losing the first run's documents.
func TestRegression_WriterRestartKeepsExistingSegments(t *testing.T) {
	dir, tmp := t.TempDir(), t.TempDir()

	w1 := &textiplex.Writer{Directory: dir, TemporaryDirectory: tmp}
	if err := w1.Batch(mkBatch(manyDocs("first-run", 100, "alpha")...)); err != nil {
		t.Fatal(err)
	}

	// process restarts, same directory
	w2 := &textiplex.Writer{Directory: dir, TemporaryDirectory: tmp}
	if err := w2.Batch(mkBatch(manyDocs("second-run", 5, "beta")...)); err != nil {
		t.Fatal(err)
	}
	if err := w2.Merge(); err != nil {
		t.Fatal(err)
	}

	n, err := countDocs(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if alpha := query(t, r, "+tag:alpha"); n != 105 || len(alpha) != 100 {
		t.Fatalf("docs=%d (want 105), first-run hits=%d (want 100)", n, len(alpha))
	}
}

// Was: a Writer in a fresh process named its merge output after its own first
// input, truncating it mid-merge: a crash plus 100 documents lost.
func TestRegression_MergeInFreshProcessKeepsItsInputs(t *testing.T) {
	if isChild() {
		dir, tmp := os.Getenv("POC_DIR"), os.Getenv("POC_TMP")
		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: tmp, MaxWorkers: 1} // fresh process
		err := w.Merge()
		fmt.Printf("RESULT Writer.Merge err=%v\n", err)
		n, lerr := countDocs(dir)
		fmt.Printf("RESULT docs=%d err=%v\n", n, lerr)
		return
	}
	dir, tmp := t.TempDir(), t.TempDir()
	w1 := &textiplex.Writer{Directory: dir, TemporaryDirectory: tmp}
	for i := range 2 {
		if err := w1.Batch(mkBatch(manyDocs(fmt.Sprintf("seg%d", i), 100, "alpha")...)); err != nil {
			t.Fatal(err)
		}
	}
	out, ee := runChild(t, t.Name(), "POC_DIR="+dir, "POC_TMP="+tmp)
	requireChildOK(t, out, ee, "RESULT Writer.Merge err=<nil>", "RESULT docs=200 err=<nil>")
}

// Was: Merge ignored its final flush error (disk full) and returned success
// with a truncated file; Writer then deleted both inputs.
func TestRegression_MergeWriteErrorKeepsInputs(t *testing.T) {
	if isChild() {
		dir, tmp := os.Getenv("POC_DIR"), os.Getenv("POC_TMP")
		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: tmp}
		_ = w.Batch(mkBatch(manyDocs("a", 3000, "alpha", "gamma")...))
		_ = w.Batch(mkBatch(manyDocs("b", 3000, "beta", "gamma")...))

		// Measure the size the merged segment needs, on a scratch copy.
		segs := segmentFiles(t, dir)
		var a, b storage.Storage
		_ = a.Load(filepath.Join(dir, segs[0]))
		_ = b.Load(filepath.Join(dir, segs[1]))
		probe := filepath.Join(tmp, "probe.seg")
		if err := (&storage.Merger{TempDir: tmp}).Merge(probe, &a, &b); err != nil {
			t.Fatal(err)
		}
		a.Close()
		b.Close()
		info, _ := os.Stat(probe)
		os.Remove(probe)

		// "Disk" is 1 byte short of what the merged file needs.
		setFileSizeLimit(t, uint64(info.Size()-1))
		err := w.Merge()
		fmt.Printf("RESULT merge failed=%v\n", err != nil)

		segs = segmentFiles(t, dir)
		fmt.Printf("RESULT segments=%d\n", len(segs))
		total := 0
		for _, name := range segs {
			var s storage.Storage
			if err := s.Load(filepath.Join(dir, name)); err != nil {
				fmt.Printf("RESULT load %s err=%v\n", name, err)
				continue
			}
			if err := s.Verify(); err != nil {
				fmt.Printf("RESULT verify %s err=%v\n", name, err)
			}
			total += len(s.DocumentsIds)
			s.Close()
		}
		fmt.Printf("RESULT total docs=%d\n", total)
		return
	}

	out, ee := runChild(t, t.Name(), "POC_DIR="+t.TempDir(), "POC_TMP="+t.TempDir())
	requireChildOK(t, out, ee, "RESULT merge failed=true", "RESULT segments=2", "RESULT total docs=6000")
	if strings.Contains(out, "RESULT load") || strings.Contains(out, "RESULT verify") {
		t.Fatalf("an input was damaged:\n%s", out)
	}
}

// Was: SaveTo truncated the destination first and removed it on error, so a
// failed rewrite deleted the previous good index.
func TestRegression_SaveToFailureKeepsPreviousIndex(t *testing.T) {
	if isChild() {
		path := filepath.Join(os.Getenv("POC_DIR"), "index.bin")
		saveIndex(t, path, manyDocs("old", 50, "alpha")...)
		info, _ := os.Stat(path)

		setFileSizeLimit(t, uint64(info.Size()*4))
		var s storage.Storage
		_ = s.BuildFrom(manyDocs("new", 5000, "alpha", "beta")...) // larger than the limit
		err := s.SaveTo(path)
		fmt.Printf("RESULT SaveTo failed=%v\n", err != nil)

		var old storage.Storage
		lerr := old.Load(path)
		fmt.Printf("RESULT old index load err=%v docs=%d\n", lerr, len(old.DocumentsIds))
		leftovers, _ := filepath.Glob(filepath.Join(os.Getenv("POC_DIR"), ".*.tmp"))
		fmt.Printf("RESULT temp files left=%d\n", len(leftovers))
		return
	}
	out, ee := runChild(t, t.Name(), "POC_DIR="+t.TempDir())
	requireChildOK(t, out, ee, "RESULT SaveTo failed=true", "RESULT old index load err=<nil> docs=50", "RESULT temp files left=0")
}

// Was: no crash recovery; a crash after the merged output appeared but before
// the inputs were deleted left documents duplicated. Now Merge records its
// inputs and finishes the job on the next run.
func TestRegression_MergeRecoversAfterCrash(t *testing.T) {
	buildPair := func(t *testing.T) (dir string, segs []string) {
		dir = t.TempDir()
		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: t.TempDir()}
		for i := range 2 {
			if err := w.Batch(mkBatch(manyDocs(fmt.Sprintf("s%d", i), 50, "alpha")...)); err != nil {
				t.Fatal(err)
			}
		}
		return dir, segmentFiles(t, dir)
	}

	t.Run("crash after output committed", func(t *testing.T) {
		dir, segs := buildPair(t)
		var a, b storage.Storage
		a.Load(filepath.Join(dir, segs[0]))
		b.Load(filepath.Join(dir, segs[1]))
		out := filepath.Join(dir, "00000000000000FF.seg")
		os.WriteFile(out+".parents", []byte(segs[0]+"\n"+segs[1]+"\n"), 0o644)
		if err := (&storage.Merger{TempDir: t.TempDir()}).Merge(out, &a, &b); err != nil {
			t.Fatal(err)
		}
		a.Close()
		b.Close()
		// crash here: inputs, output and record all present

		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: t.TempDir()}
		if err := w.Merge(); err != nil {
			t.Fatal(err)
		}
		if n, err := countDocs(dir); err != nil || n != 100 {
			t.Fatalf("docs=%d err=%v, want 100 (no duplicates)", n, err)
		}
		if _, err := os.Stat(out + ".parents"); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("merge record left behind")
		}
	})

	t.Run("crash before output committed", func(t *testing.T) {
		dir, segs := buildPair(t)
		out := filepath.Join(dir, "00000000000000FF.seg")
		os.WriteFile(out+".parents", []byte(segs[0]+"\n"+segs[1]+"\n"), 0o644)
		os.WriteFile(filepath.Join(dir, ".00000000000000FF.seg.123.tmp"), make([]byte, 4096), 0o644) // partial output

		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: t.TempDir()}
		if err := w.Merge(); err != nil {
			t.Fatal(err)
		}
		if n, err := countDocs(dir); err != nil || n != 100 {
			t.Fatalf("docs=%d err=%v, want 100", n, err)
		}
	})

	t.Run("record naming a non-segment is refused", func(t *testing.T) {
		dir, _ := buildPair(t)
		victim := filepath.Join(dir, "precious.txt")
		os.WriteFile(victim, []byte("keep me"), 0o644)
		saveIndex(t, filepath.Join(dir, "00000000000000FF.seg"), mkDoc("x", "alpha"))
		os.WriteFile(filepath.Join(dir, "00000000000000FF.seg.parents"), []byte("precious.txt\n"), 0o644)

		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: t.TempDir()}
		if err := w.Merge(); err == nil {
			t.Fatal("merge accepted a record naming a non-segment")
		}
		if _, err := os.Stat(victim); err != nil {
			t.Fatal("non-segment file was deleted")
		}
	})
}

// Was: Writer.Merge on an empty directory spun forever.
func TestRegression_MergeEmptyDirectoryReturns(t *testing.T) {
	w := &textiplex.Writer{Directory: t.TempDir(), TemporaryDirectory: t.TempDir()}
	done := make(chan error, 1)
	go func() { done <- w.Merge() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Writer.Merge() on an empty directory did not return")
	}
}

// ------------------------------------------ 2. memory-safety / lifetime ------

// Was: re-saving a loaded index to its own path truncated the mapped file:
// SIGBUS and a 0-byte index.
func TestRegression_SaveToOverMappedFile(t *testing.T) {
	if isChild() {
		path := filepath.Join(os.Getenv("POC_DIR"), "index.bin")
		var s storage.Storage
		if err := s.Load(path); err != nil {
			t.Fatal(err)
		}
		err := s.SaveTo(path)
		fmt.Printf("RESULT SaveTo err=%v\n", err)
		fmt.Printf("RESULT old mapping still readable: %s\n", s.DocumentsIds[len(s.DocumentsIds)-1].Value.UnsafeString())
		s.Close()
		var again storage.Storage
		lerr := again.Load(path)
		fmt.Printf("RESULT reload err=%v docs=%d verify=%v\n", lerr, len(again.DocumentsIds), again.Verify())
		return
	}
	dir := t.TempDir()
	saveIndex(t, filepath.Join(dir, "index.bin"), manyDocs("doc", 2000, "alpha")...)
	out, ee := runChild(t, t.Name(), "POC_DIR="+dir)
	requireChildOK(t, out, ee, "RESULT SaveTo err=<nil>", "RESULT old mapping still readable: doc-001999",
		"RESULT reload err=<nil> docs=2000 verify=<nil>")
}

// Was: ids yielded by Reader.QueryString pointed into the mapping; after Close
// they were dangling (SIGSEGV).
func TestRegression_DocIdsSurviveClose(t *testing.T) {
	if isChild() {
		dir := os.Getenv("POC_DIR")
		r := newReader()
		if err := r.Reset(dir); err != nil {
			t.Fatal(err)
		}
		seq, _ := r.QueryString(0, textiplex.SortFieldBM25, false, "+tag:alpha")
		var kept [][]byte
		for id := range seq {
			kept = append(kept, id)
			if len(kept) == 3 {
				_ = r.Close() // closing from inside the loop must neither deadlock nor crash
			}
		}
		fmt.Printf("RESULT kept=%d first=%s\n", len(kept), kept[0])
		return
	}
	dir := t.TempDir()
	saveIndex(t, filepath.Join(dir, "0.seg"), manyDocs("doc", 10, "alpha")...)
	out, ee := runChild(t, t.Name(), "POC_DIR="+dir)
	requireChildOK(t, out, ee, "RESULT kept=3 first=doc-0000")
}

// Queries racing Close and Reset must neither crash nor read a closed index.
// Run with -race.
func TestRegression_ConcurrentQueriesAndReset(t *testing.T) {
	if isChild() {
		dir := os.Getenv("POC_DIR")
		r := newReader()
		if err := r.Reset(dir); err != nil {
			t.Fatal(err)
		}
		stop := make(chan struct{})
		done := make(chan int)
		for range 8 {
			go func() {
				n := 0
				for {
					select {
					case <-stop:
						done <- n
						return
					default:
					}
					seq, err := r.QueryString(0, textiplex.SortFieldBM25, false, "+tag:alpha")
					if err != nil {
						continue // closed between Reset calls
					}
					for id := range seq {
						if !strings.HasPrefix(string(id), "doc-") {
							panic("bad id " + string(id))
						}
						n++
					}
				}
			}()
		}
		for i := range 200 {
			if i%2 == 0 {
				r.Close()
			} else if err := r.Reset(dir); err != nil {
				t.Fatal(err)
			}
		}
		close(stop)
		total := 0
		for range 8 {
			total += <-done
		}
		fmt.Printf("RESULT ok ids=%v\n", total > 0)
		return
	}
	dir := t.TempDir()
	saveIndex(t, filepath.Join(dir, "0.seg"), manyDocs("doc", 500, "alpha")...)
	out, ee := runChild(t, t.Name(), "POC_DIR="+dir)
	requireChildOK(t, out, ee, "RESULT ok")
}

// Was: a failed Load unmapped the file but left DocumentsIds pointing into it.
func TestRegression_FailedLoadLeavesStorageEmpty(t *testing.T) {
	dir := t.TempDir()
	full := filepath.Join(dir, "full.bin")
	saveIndex(t, full, manyDocs("doc", 100, "alpha")...)
	data, _ := os.ReadFile(full)
	// Keep header + doc ids + token freqs, drop the field blocks (and footer).
	cut := int(storage.HeaderSize) + 100*int(storage.DocumentIdSize) + 100*int(storage.TokenFrequencyEntrySize) + 1
	broken := filepath.Join(dir, "broken.bin")
	os.WriteFile(broken, toV1(data)[:cut], 0o600)

	var s storage.Storage
	err := s.Load(broken)
	if !errors.Is(err, storage.ErrCorrupt) {
		t.Fatalf("Load err=%v, want ErrCorrupt", err)
	}
	if s.DocumentsIds != nil || s.Buffer != nil || s.File != nil || s.Fields != nil {
		t.Fatal("failed Load left state behind")
	}
}

// ----------------------------------------------- 3. untrusted index files ---

func craftHeader(totalDocs uint32, fieldCount, totalPL, plDataSize, totalTF uint64) []byte {
	h := storage.Header{
		Magic: storage.MagicNumber, Version: storage.VersionV1, TotalDocuments: totalDocs,
		FieldCount: fieldCount, TotalPostingLists: totalPL, TotalPostingListsDataSize: plDataSize,
		TotalTokenFrequencies: totalTF,
	}
	return append([]byte(nil), unsafe.Slice((*byte)(unsafe.Pointer(&h)), unsafe.Sizeof(h))...)
}

// Was: a 57-byte file claiming 2^34 fields made Load allocate until the
// runtime aborted with a fatal out-of-memory error.
func TestRegression_TinyFileHugeFieldCount(t *testing.T) {
	if isChild() {
		lim := currentVmSize() + 1<<30
		syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: lim, Max: lim})
		var s storage.Storage
		err := s.Load(os.Getenv("POC_FILE"))
		fmt.Printf("RESULT corrupt=%v\n", errors.Is(err, storage.ErrCorrupt))
		return
	}
	file := filepath.Join(t.TempDir(), "evil.bin")
	os.WriteFile(file, append(craftHeader(0, 1<<34, 0, 0, 0), 0), 0o600)
	out, ee := runChild(t, t.Name(), "POC_FILE="+file)
	requireChildOK(t, out, ee, "RESULT corrupt=true")
}

func currentVmSize() uint64 {
	b, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "VmSize:") {
			var kb uint64
			fmt.Sscanf(strings.TrimSpace(strings.TrimPrefix(line, "VmSize:")), "%d", &kb)
			return kb * 1024
		}
	}
	return 4 << 30
}

// Was: wrapping size multiplications made Load panic.
func TestRegression_OverflowingCountsAreErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "evil.bin")
	os.WriteFile(file, append(craftHeader(0, 0, 0, 0, (1<<61)+1), make([]byte, 64)...), 0o600)

	var err error
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("Load panicked: %v", p)
			}
		}()
		var s storage.Storage
		err = s.Load(file)
	}()
	if !errors.Is(err, storage.ErrCorrupt) {
		t.Fatalf("Load err=%v, want ErrCorrupt", err)
	}
}

// Was: a flipped reference loaded fine and panicked at query time. Now the
// query degrades instead of panicking, Verify reports it (checksum on v2, the
// reference itself on v1), and Merge refuses to merge it.
func TestRegression_CorruptReference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "0.seg")
	saveIndex(t, path, manyDocs("doc", 10, "alpha")...)
	data, _ := os.ReadFile(path)
	binary.NativeEndian.PutUint64(data[tokenOffset(10)+8:], 0xFFFF_FFFF) // Token.PostingListIndex
	os.WriteFile(path, data, 0o600)

	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatalf("Load: %v", err)
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				t.Fatalf("query panicked: %v", p)
			}
		}()
		query(t, r, "+tag:alpha")
	}()
	if err := r.Storage.Verify(); !errors.Is(err, storage.ErrChecksum) {
		t.Fatalf("Verify v2 err=%v, want ErrChecksum", err)
	}
	r.Close()

	// Same damage in a v1 file (no checksum): Verify finds the reference.
	v1 := filepath.Join(t.TempDir(), "v1.seg")
	os.WriteFile(v1, toV1(data), 0o600)
	var a storage.Storage
	if err := a.Load(v1); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if err := a.Verify(); !errors.Is(err, storage.ErrCorrupt) || errors.Is(err, storage.ErrChecksum) {
		t.Fatalf("Verify v1 err=%v, want reference error", err)
	}

	var b storage.Storage
	good := filepath.Join(t.TempDir(), "good.seg")
	saveIndex(t, good, manyDocs("zz", 5, "alpha")...)
	b.Load(good)
	defer b.Close()
	out := filepath.Join(t.TempDir(), "merged.seg")
	if err := (&storage.Merger{TempDir: t.TempDir()}).Merge(out, &a, &b); !errors.Is(err, storage.ErrCorrupt) {
		t.Fatalf("Merge err=%v, want ErrCorrupt", err)
	}
	if _, err := os.Stat(out); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed merge left an output file")
	}
}

// Was: no magic/version check, so zeros or foreign files loaded as an empty
// index.
func TestRegression_LoadRejectsNonIndexFiles(t *testing.T) {
	cases := map[string][]byte{
		"zeros": make([]byte, 4096),
		"wrong magic": func() []byte {
			h := craftHeader(0, 0, 0, 0, 0)
			binary.NativeEndian.PutUint64(h, 0xDEADBEEF)
			return append(h, 0)
		}(),
		"text": []byte(strings.Repeat("not an index, just some text. ", 10)),
	}
	for name, content := range cases {
		file := filepath.Join(t.TempDir(), "f.bin")
		os.WriteFile(file, content, 0o600)
		var s storage.Storage
		if err := s.Load(file); !errors.Is(err, storage.ErrCorrupt) {
			t.Errorf("%s: Load err=%v, want ErrCorrupt", name, err)
		}
	}

	future := filepath.Join(t.TempDir(), "future.bin")
	h := craftHeader(0, 0, 0, 0, 0)
	binary.NativeEndian.PutUint16(h[8:], storage.CurrentVersion+1)
	os.WriteFile(future, append(h, make([]byte, 64)...), 0o600)
	var s storage.Storage
	if err := s.Load(future); !errors.Is(err, storage.ErrUnsupportedVersion) {
		t.Errorf("future version: Load err=%v", err)
	}
}

// Indexes written before the format change (version 1, no footer) still load,
// query and verify.
func TestRegression_Version1FilesStillLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "0.seg")
	saveIndex(t, path, manyDocs("doc", 20, "alpha")...)
	data, _ := os.ReadFile(path)
	os.WriteFile(path, toV1(data), 0o600)

	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if r.Storage.Version != storage.VersionV1 {
		t.Fatalf("version=%d", r.Storage.Version)
	}
	if hits := query(t, r, "+tag:alpha"); len(hits) != 20 {
		t.Fatalf("hits=%d", len(hits))
	}
	if err := r.Storage.Verify(); err != nil {
		t.Fatal(err)
	}
}

// ------------------------------------------- 4. silent truncation (data) ----

// Was: values over 128 bytes were cut short, making them unsearchable and
// merging distinct values. Now BuildFrom (and so Writer.Batch) refuses them.
func TestRegression_LongValuesRejected(t *testing.T) {
	long := strings.Repeat("a", storage.MaxRawValueSize+1)
	exact := strings.Repeat("b", storage.MaxRawValueSize)

	var s storage.Storage
	if err := s.BuildFrom(mkDoc("id", long)); !errors.Is(err, storage.ErrValueTooLong) {
		t.Fatalf("long token: err=%v", err)
	}
	if s.DocumentsIds != nil {
		t.Fatal("failed BuildFrom left documents behind")
	}
	if err := s.BuildFrom(mkDoc(long, "x")); !errors.Is(err, storage.ErrValueTooLong) {
		t.Fatalf("long doc id: err=%v", err)
	}
	if err := s.BuildFrom(mkDoc(exact, exact)); err != nil {
		t.Fatalf("128-byte values must be accepted: %v", err)
	}

	w := &textiplex.Writer{Directory: t.TempDir(), TemporaryDirectory: t.TempDir()}
	if err := w.Batch(mkBatch(mkDoc("id", long))); !errors.Is(err, storage.ErrValueTooLong) {
		t.Fatalf("Writer.Batch err=%v", err)
	}
}

// Duplicate tokens in one document's field used to desynchronise the posting
// list from the frequencies; they are now summed.
func TestRegression_DuplicateTokensAreSummed(t *testing.T) {
	var s storage.Storage
	if err := s.BuildFrom(mkDoc("d1", "alpha", "alpha", "beta"), mkDoc("d2", "alpha")); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
	tok, ok := s.Fields[xxh3.HashString(tagField)].Tokens.GetString("alpha")
	if !ok {
		t.Fatal("alpha missing")
	}
	freqs := s.FrequenciesOf(tok)
	if len(freqs) != 2 || freqs[0].Frequency != 2 || freqs[1].Frequency != 1 {
		t.Fatalf("freqs=%+v", freqs)
	}
}

// --------------------------------------------- 5. adjacent (reader.go) ------

// Was: reverse=true with skip > hits computed uint(len)-skip and panicked.
func TestRegression_ReverseSkipPastEnd(t *testing.T) {
	dir := t.TempDir()
	saveIndex(t, filepath.Join(dir, "0.seg"), manyDocs("doc", 3, "alpha")...)
	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	seq, _ := r.QueryString(10, textiplex.SortFieldBM25, true, "+tag:alpha")
	n := 0
	for range seq {
		n++
	}
	if n != 0 {
		t.Fatalf("got %d results", n)
	}
	seq, _ = r.QueryString(1, textiplex.SortFieldBM25, true, "+tag:alpha")
	n = 0
	for range seq {
		n++
	}
	if n != 2 {
		t.Fatalf("skip=1 reverse: got %d results, want 2", n)
	}
}

// ------------------------------------------------------------- strace hook ---

// Run under strace to check the syscall order of Writer.Merge: the merged
// file and the directory are synced before the inputs are unlinked.
func TestStraceMerge(t *testing.T) {
	dir := os.Getenv("POC_STRACE_DIR")
	if dir == "" {
		t.Skip("set POC_STRACE_DIR")
	}
	w := &textiplex.Writer{Directory: dir, TemporaryDirectory: os.Getenv("POC_STRACE_TMP"), MaxWorkers: 1}
	if err := w.Merge(); err != nil {
		t.Fatal(err)
	}
}

// Helper for the strace run: writes two segments into POC_MKSEG_DIR.
func TestMakeSegments(t *testing.T) {
	dir := os.Getenv("POC_MKSEG_DIR")
	if dir == "" {
		t.Skip("set POC_MKSEG_DIR")
	}
	w := &textiplex.Writer{Directory: dir, TemporaryDirectory: t.TempDir()}
	for i := range 2 {
		if err := w.Batch(mkBatch(manyDocs(fmt.Sprintf("seg%d", i), 2000, "alpha", "beta")...)); err != nil {
			t.Fatal(err)
		}
	}
}
