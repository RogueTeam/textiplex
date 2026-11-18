// Proof-of-concept tests for storage-layer vulnerabilities and data-loss paths.
//
// Tests that are expected to kill the process (SIGSEGV, SIGBUS, fatal OOM) or
// that need process-wide resource limits re-execute the test binary as a child
// (POC_CHILD=1) and assert on the child's exit status and output.
package reviews_test

import (
	"bytes"
	"encoding/binary"
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

func saveIndex(t *testing.T, path string, docs ...*storage.Document) {
	t.Helper()
	var s storage.Storage
	s.BuildFrom(docs...)
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

// query returns copies of the matching doc ids.
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

func countDocs(t *testing.T, dir string) (n int, err error) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	if len(entries) != 1 {
		return 0, fmt.Errorf("expected one segment, got %d", len(entries))
	}
	var s storage.Storage
	if err := s.Load(filepath.Join(dir, entries[0].Name())); err != nil {
		return 0, err
	}
	defer s.Close()
	return len(s.DocumentsIds), nil
}

func setFileSizeLimit(t *testing.T, limit uint64) {
	// Go ignores SIGXFSZ by default, so writes past the limit fail with EFBIG,
	// which is exactly how ENOSPC / quota errors surface to the code under test.
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: limit, Max: limit}); err != nil {
		t.Fatalf("setrlimit: %v", err)
	}
}

// ------------------------------------------------------- 1. data loss -------

// Writer names segments from an in-memory counter that starts at 0 and opens
// them with os.Create (O_TRUNC). A second Writer on the same directory (e.g.
// after a process restart) silently overwrites existing segments.
func TestPoC_WriterRestartOverwritesExistingSegment(t *testing.T) {
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

	n, err := countDocs(t, dir)
	if err != nil {
		t.Fatal(err)
	}
	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	alpha := query(t, r, "+tag:alpha")
	t.Logf("docs in index after restart: %d (expected 105); hits for first-run token: %d (expected 100)", n, len(alpha))
	if n == 105 && len(alpha) == 100 {
		t.Fatal("no data loss observed")
	}
}

// Merger.Merge ignores the error of its final bufio Flush. If the disk fills
// up (simulated with RLIMIT_FSIZE -> EFBIG) Merge returns nil with a truncated
// output, and Writer.Merge then deletes both input segments.
func TestPoC_MergeSwallowsWriteErrorAndWriterDeletesInputs(t *testing.T) {
	if isChild() {
		dir, tmp := os.Getenv("POC_DIR"), os.Getenv("POC_TMP")
		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: tmp}
		_ = w.Batch(mkBatch(manyDocs("a", 3000, "alpha", "gamma")...))
		_ = w.Batch(mkBatch(manyDocs("b", 3000, "beta", "gamma")...))

		// Measure the size the merged segment needs, on a scratch copy.
		entries, _ := os.ReadDir(dir)
		var a, b storage.Storage
		_ = a.Load(filepath.Join(dir, entries[0].Name()))
		_ = b.Load(filepath.Join(dir, entries[1].Name()))
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
		fmt.Printf("RESULT Writer.Merge err=%v\n", err)

		entries, _ = os.ReadDir(dir)
		fmt.Printf("RESULT segments left=%d\n", len(entries))
		var merged storage.Storage
		fmt.Printf("RESULT load merged err=%v\n", merged.Load(filepath.Join(dir, entries[0].Name())))
		return
	}

	out, ee := runChild(t, t.Name(), "POC_DIR="+t.TempDir(), "POC_TMP="+t.TempDir())
	t.Log(out)
	if ee != nil {
		t.Fatalf("child failed: %s", signaled(ee))
	}
	if !strings.Contains(out, "RESULT Writer.Merge err=<nil>") ||
		!strings.Contains(out, "RESULT segments left=1") ||
		strings.Contains(out, "RESULT load merged err=<nil>") {
		t.Fatal("did not reproduce")
	}
}

// SaveTo opens the destination with os.Create (truncating it in place) and
// removes the file on any error. Rewriting an existing index and hitting an
// error mid-write (disk full, quota, EIO) deletes the previous good index.
func TestPoC_SaveToFailureDeletesPreviousIndex(t *testing.T) {
	if isChild() {
		path := filepath.Join(os.Getenv("POC_DIR"), "index.bin")
		saveIndex(t, path, manyDocs("old", 50, "alpha")...)
		info, _ := os.Stat(path)
		fmt.Printf("RESULT old index size=%d\n", info.Size())

		setFileSizeLimit(t, uint64(info.Size()*4))
		var s storage.Storage
		s.BuildFrom(manyDocs("new", 5000, "alpha", "beta")...) // larger than the limit
		err := s.SaveTo(path)
		fmt.Printf("RESULT SaveTo err=%v\n", err)
		_, statErr := os.Stat(path)
		fmt.Printf("RESULT old index exists=%v\n", statErr == nil)
		return
	}
	out, ee := runChild(t, t.Name(), "POC_DIR="+t.TempDir())
	t.Log(out)
	if ee != nil || !strings.Contains(out, "RESULT old index exists=false") {
		t.Fatal("did not reproduce")
	}
}

// ------------------------------------------ 2. memory-safety / lifetime ------

// os.Create truncates a file that is still mmap'd (MAP_SHARED) by a loaded
// Storage. Touching the mapping afterwards raises SIGBUS. Here the loaded
// storage is re-saved to its own path (an in-place rewrite): the process dies
// half-way and the index on disk is destroyed.
func TestPoC_SaveToOverMappedFileSIGBUS(t *testing.T) {
	path := filepath.Join(os.Getenv("POC_DIR"), "index.bin")
	if isChild() {
		var s storage.Storage
		if err := s.Load(path); err != nil {
			t.Fatal(err)
		}
		_ = s.SaveTo(path)
		fmt.Println("RESULT survived")
		return
	}
	dir := t.TempDir()
	path = filepath.Join(dir, "index.bin")
	saveIndex(t, path, manyDocs("doc", 2000, "alpha")...)
	before, _ := os.Stat(path)

	out, ee := runChild(t, t.Name(), "POC_DIR="+dir)
	after, _ := os.Stat(path)
	t.Logf("child: %s\n%s", signaled(ee), faultLines(out))
	t.Logf("index size before=%d after=%d", before.Size(), after.Size())
	if signaled(ee) != "exit 2" || !strings.Contains(out, "SIGBUS") {
		t.Fatal("did not reproduce SIGBUS")
	}
}

// Doc ids yielded by Reader.QueryString alias the mmap. After Close (or a
// Reset to swap in a freshly merged index) any retained id is a dangling
// pointer into unmapped memory: the process gets SIGSEGV, which recover()
// cannot catch.
func TestPoC_DocIdUseAfterCloseSIGSEGV(t *testing.T) {
	if isChild() {
		dir := os.Getenv("POC_DIR")
		r := newReader()
		if err := r.Reset(dir); err != nil {
			t.Fatal(err)
		}
		seq, _ := r.QueryString(0, textiplex.SortFieldBM25, false, "+tag:alpha")
		var kept [][]byte
		for id := range seq {
			kept = append(kept, id) // e.g. cached, or handed to another goroutine
		}
		_ = r.Close()
		defer func() { fmt.Println("RESULT recovered:", recover()) }()
		fmt.Println("RESULT id after close:", string(kept[0]))
		return
	}
	dir := t.TempDir()
	saveIndex(t, filepath.Join(dir, "0.seg"), manyDocs("doc", 10, "alpha")...)
	out, ee := runChild(t, t.Name(), "POC_DIR="+dir)
	t.Logf("child: %s\n%s", signaled(ee), faultLines(out))
	if !strings.Contains(out, "SIGSEGV") || strings.Contains(out, "RESULT recovered") {
		t.Fatal("did not reproduce")
	}
}

// When Load fails after it has already mapped some tables, the deferred
// Munmap releases the region but s.DocumentsIds / s.TokenFrequencies keep
// pointing into it.
func TestPoC_FailedLoadLeavesDanglingSlices(t *testing.T) {
	if isChild() {
		var s storage.Storage
		err := s.Load(os.Getenv("POC_FILE"))
		fmt.Printf("RESULT load err=%v len(DocumentsIds)=%d\n", err, len(s.DocumentsIds))
		fmt.Println("RESULT first id:", s.DocumentsIds[0].Value.UnsafeString())
		return
	}
	dir := t.TempDir()
	full := filepath.Join(dir, "full.bin")
	saveIndex(t, full, manyDocs("doc", 100, "alpha")...)
	data, _ := os.ReadFile(full)
	// Keep header + doc ids + token freqs, drop the field blocks.
	cut := int(storage.HeaderSize) + 100*int(storage.DocumentIdSize) + 100*int(storage.TokenFrequencyEntrySize) + 1
	broken := filepath.Join(dir, "broken.bin")
	os.WriteFile(broken, data[:cut], 0o600)

	out, ee := runChild(t, t.Name(), "POC_FILE="+broken)
	t.Logf("child: %s\n%s", signaled(ee), faultLines(out))
	if !strings.Contains(out, "len(DocumentsIds)=100") || !strings.Contains(out, "SIGSEGV") {
		t.Fatal("did not reproduce")
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

// A 57-byte file claiming 2^34 fields makes Load call
// make(map[uint64]*Field, 2^34). The runtime aborts with a fatal
// out-of-memory error (not a panic: recover() does not help).
// The child runs under RLIMIT_AS so the sandbox is not exhausted.
func TestPoC_TinyFileFatalOOM(t *testing.T) {
	if isChild() {
		lim := currentVmSize() + 1<<30
		syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: lim, Max: lim})
		defer func() { fmt.Println("RESULT recovered:", recover()) }()
		var s storage.Storage
		err := s.Load(os.Getenv("POC_FILE"))
		fmt.Println("RESULT load err:", err)
		return
	}
	file := filepath.Join(t.TempDir(), "evil.bin")
	os.WriteFile(file, append(craftHeader(0, 1<<34, 0, 0, 0), 0), 0o600)
	fi, _ := os.Stat(file)
	out, ee := runChild(t, t.Name(), "POC_FILE="+file)
	t.Logf("file size=%d bytes; child: %s\n%s", fi.Size(), signaled(ee), firstLines(out, 4))
	if !strings.Contains(out, "out of memory") || strings.Contains(out, "RESULT recovered") {
		t.Fatal("did not reproduce")
	}
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

// Size computations in Load are 64-bit multiplications that can wrap.
// unsafe.Slice catches the wrap, so the result is a runtime panic out of Load
// rather than an error.
func TestPoC_LoadPanicsOnOverflowingCounts(t *testing.T) {
	file := filepath.Join(t.TempDir(), "evil.bin")
	body := make([]byte, 64)
	os.WriteFile(file, append(craftHeader(0, 0, 0, 0, (1<<61)+1), body...), 0o600)

	var panicked any
	var err error
	func() {
		defer func() { panicked = recover() }()
		var s storage.Storage
		err = s.Load(file)
	}()
	t.Logf("Load err=%v panic=%v", err, panicked)
	if panicked == nil {
		t.Fatal("no panic")
	}
}

// Load never checks cross-references inside the file (token -> posting list,
// token -> frequency range, posting-list doc ids -> doc table). One flipped
// field in an otherwise valid index loads fine and panics at query time.
func TestPoC_CorruptReferencePanicsAtQueryTime(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "0.seg")
	saveIndex(t, path, manyDocs("doc", 10, "alpha")...)

	data, _ := os.ReadFile(path)
	// header | 10 doc ids | 10 TF entries | field header | 10 doc lengths | token[0]
	tokOff := int(storage.HeaderSize) + 10*int(storage.DocumentIdSize) + 10*int(storage.TokenFrequencyEntrySize) +
		int(storage.FieldHeaderSize) + 10*int(storage.DocumentLengthEntrySize)
	binary.LittleEndian.PutUint64(data[tokOff+8:], 0xFFFF_FFFF) // Token.PostingListIndex
	os.WriteFile(path, data, 0o600)

	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatalf("Load rejected the corrupt file (good): %v", err)
	}
	defer r.Close()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		query(t, r, "+tag:alpha")
	}()
	t.Logf("Load accepted file; query panic=%v", panicked)
	if panicked == nil {
		t.Fatal("no panic")
	}
}

// No magic / version / checksum validation: arbitrary bytes load as a valid
// (empty) index, e.g. a merge output whose header was never written.
func TestPoC_LoadAcceptsNonIndexFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "zeros.bin")
	os.WriteFile(file, make([]byte, 4096), 0o600)
	var s storage.Storage
	err := s.Load(file)
	defer s.Close()
	t.Logf("Load(4 KiB of zeros) err=%v docs=%d", err, len(s.DocumentsIds))

	file2 := filepath.Join(t.TempDir(), "wrongmagic.bin")
	h := craftHeader(0, 0, 0, 0, 0)
	binary.LittleEndian.PutUint64(h, 0xDEADBEEF)
	binary.LittleEndian.PutUint16(h[8:], 999)
	os.WriteFile(file2, append(h, 0), 0o600)
	var s2 storage.Storage
	err2 := s2.Load(file2)
	defer s2.Close()
	t.Logf("Load(magic=0xDEADBEEF, version=999) err=%v", err2)
	if err != nil || err2 != nil {
		t.Fatal("did not reproduce")
	}
}

// ------------------------------------------- 4. silent truncation (data) ----

// Tokens and doc ids are stored in a fixed 128-byte RawValue. RawValueFrom
// truncates silently, so:
//   - a >128-byte keyword can never be found by its real value
//   - two values sharing a 128-byte prefix collapse; one posting list is shadowed
//   - two doc ids sharing a 128-byte prefix become indistinguishable
func TestPoC_LongValuesSilentlyTruncated(t *testing.T) {
	prefix := strings.Repeat("a", 128)
	longX, longY := prefix+"-x", prefix+"-y"
	idPrefix := "https://example.com/" + strings.Repeat("p", 120)

	dir := t.TempDir()
	saveIndex(t, filepath.Join(dir, "0.seg"),
		mkDoc(idPrefix+"/doc-1", longX),
		mkDoc(idPrefix+"/doc-2", longY),
	)
	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	tokens := r.Storage.Fields[xxh3.HashString(tagField)].Tokens
	t.Logf("token table: %d entries, identical values: %v", len(tokens),
		bytes.Equal(tokens[0].Value.Bytes(), tokens[1].Value.Bytes()))
	hitsX := query(t, r, "+tag:"+longX)
	hitsPrefix := query(t, r, "+tag:"+prefix)
	t.Logf("query full value  -> %d hits (expected 1)", len(hitsX))
	t.Logf("query 128B prefix -> %d hits (2 docs carry it)", len(hitsPrefix))
	t.Logf("doc ids equal after truncation: %v",
		r.Storage.DocumentsIds[0].Value.UnsafeString() == r.Storage.DocumentsIds[1].Value.UnsafeString())
	if len(hitsX) == 1 {
		t.Fatal("did not reproduce")
	}
}

// --------------------------------------------- 5. adjacent (reader.go) ------

// Reader.Query with reverse=true computes uint(len(scores))-skip, which wraps
// when skip > len(scores): a caller-controlled pagination offset panics.
func TestPoC_ReverseSkipUnderflowPanics(t *testing.T) {
	dir := t.TempDir()
	saveIndex(t, filepath.Join(dir, "0.seg"), manyDocs("doc", 3, "alpha")...)
	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var panicked any
	func() {
		defer func() { panicked = recover() }()
		seq, _ := r.QueryString(10, textiplex.SortFieldBM25, true, "+tag:alpha")
		for range seq {
		}
	}()
	t.Logf("skip=10 reverse=true over 3 hits: panic=%v", panicked)
	if panicked == nil {
		t.Fatal("no panic")
	}
}

// ------------------------------------------------------------- strace hook ---

// Run under strace to observe the syscall order of Writer.Merge (see report).
func TestStraceMerge(t *testing.T) {
	dir := os.Getenv("POC_STRACE_DIR")
	if dir == "" {
		t.Skip("set POC_STRACE_DIR")
	}
	w := &textiplex.Writer{Directory: dir, TemporaryDirectory: os.Getenv("POC_STRACE_TMP"), MaxWorkers: 1}
	w.SegmentCounter.Store(1000) // avoid the name-collision bug so the trace shows the happy path
	if err := w.Merge(); err != nil {
		t.Fatal(err)
	}
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

func faultLines(s string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, "RESULT") || strings.Contains(l, "fatal error") ||
			strings.Contains(l, "unexpected fault") || strings.Contains(l, "[signal ") {
			keep = append(keep, strings.TrimSpace(l))
		}
	}
	return strings.Join(keep, "\n")
}

// A Writer that merges segments written by an earlier process starts its
// counter at 0, so the merge output name collides with an *input* segment.
// os.Create truncates the input while it is mmap'd and being read.
func TestPoC_MergeInFreshProcessTruncatesItsOwnInput(t *testing.T) {
	if isChild() {
		dir, tmp := os.Getenv("POC_DIR"), os.Getenv("POC_TMP")
		w := &textiplex.Writer{Directory: dir, TemporaryDirectory: tmp, MaxWorkers: 1} // fresh process
		err := w.Merge()
		fmt.Printf("RESULT Writer.Merge err=%v\n", err)
		n, lerr := countDocs(t, dir)
		fmt.Printf("RESULT docs after merge=%d load err=%v\n", n, lerr)
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
	entries, _ := os.ReadDir(dir)
	t.Logf("child: %s (expected 200 docs)\n%s\nsegments left: %d", signaled(ee), firstLines(grepLines(out, "RESULT|panic|fatal|signal|goroutine 1"), 6), len(entries))
	var segA storage.Storage
	lerr := segA.Load(filepath.Join(dir, "0000000000000001.seg"))
	t.Logf("input segment 1 after crash: load err=%v docs=%d (was 100)", lerr, len(segA.DocumentsIds))
	t.Logf("panic site:\n%s", firstLines(grepLines(out, "textiplex/storage."), 2))
	if ee == nil && strings.Contains(out, "docs after merge=200 ") {
		t.Fatal("no data loss")
	}
}

func grepLines(s, pattern string) string {
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		for _, p := range strings.Split(pattern, "|") {
			if strings.Contains(l, p) {
				keep = append(keep, strings.TrimSpace(l))
				break
			}
		}
	}
	return strings.Join(keep, "\n")
}

// Writer.Merge on an empty directory never returns: zero entries is neither
// "done" (==1) nor an error, so the loop spins on os.ReadDir forever.
func TestPoC_MergeEmptyDirectorySpinsForever(t *testing.T) {
	w := &textiplex.Writer{Directory: t.TempDir(), TemporaryDirectory: t.TempDir()}
	done := make(chan error, 1)
	go func() { done <- w.Merge() }()
	select {
	case err := <-done:
		t.Fatalf("returned: %v", err)
	case <-time.After(2 * time.Second):
		t.Log("Writer.Merge() on an empty directory still running after 2s (busy loop)")
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
