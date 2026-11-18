package reviews_test

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/RogueTeam/textiplex"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/zeebo/xxh3"
)

// Regression tests for problems found by the second review of the fixes.

func umask() os.FileMode {
	old := syscall.Umask(0)
	syscall.Umask(old)
	return os.FileMode(old)
}

// The first fix created files with os.CreateTemp (0600), so segments became
// unreadable to other users. New files must get 0666 minus the umask, like
// os.Create, and a replaced file must keep its permissions.
func TestRegression_SavedFilePermissions(t *testing.T) {
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.seg")
	saveIndex(t, fresh, mkDoc("a", "x"))
	info, _ := os.Stat(fresh)
	if want := 0o666 &^ umask(); info.Mode().Perm() != want {
		t.Fatalf("new file mode %v, want %v", info.Mode().Perm(), want)
	}

	if err := os.Chmod(fresh, 0o640); err != nil {
		t.Fatal(err)
	}
	saveIndex(t, fresh, mkDoc("b", "y"))
	info, _ = os.Stat(fresh)
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("replaced file mode %v, want 0640", info.Mode().Perm())
	}

	w := &textiplex.Writer{Directory: t.TempDir(), TemporaryDirectory: t.TempDir()}
	if err := w.Batch(mkBatch(mkDoc("c", "z"))); err != nil {
		t.Fatal(err)
	}
	segs := segmentFiles(t, w.Directory)
	info, _ = os.Stat(filepath.Join(w.Directory, segs[0]))
	if want := 0o666 &^ umask(); info.Mode().Perm() != want {
		t.Fatalf("segment mode %v, want %v", info.Mode().Perm(), want)
	}
}

// SaveTo through a symlink updates the target, as os.Create did, instead of
// replacing the link with a regular file.
func TestRegression_SaveToThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "v1.seg")
	link := filepath.Join(dir, "current.seg")
	saveIndex(t, target, mkDoc("old", "x"))
	if err := os.Symlink("v1.seg", link); err != nil {
		t.Fatal(err)
	}
	saveIndex(t, link, mkDoc("new", "x"), mkDoc("new2", "x"))

	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link replaced by a regular file (mode %v, err %v)", fi.Mode(), err)
	}
	var s storage.Storage
	if err := s.Load(target); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if len(s.DocumentsIds) != 2 {
		t.Fatalf("target holds %d docs, want the new 2", len(s.DocumentsIds))
	}
}

// The first fix held the reader's lock without defer during scoring: one panic
// in a query left it locked and every later Close/Reset hung forever.
func TestRegression_ReaderUsableAfterQueryPanic(t *testing.T) {
	dir := t.TempDir()
	saveIndex(t, filepath.Join(dir, "0.seg"), manyDocs("doc", 5, "alpha")...)
	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatal(err)
	}
	r.Searcher.Storage = nil // stands in for any bug that panics while scoring

	func() {
		defer func() { recover() }()
		seq, _ := r.QueryString(0, textiplex.SortFieldBM25, false, "+tag:alpha")
		for range seq {
		}
	}()

	done := make(chan error, 1)
	go func() { done <- r.Reset(dir) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Reset deadlocked after a query panic")
	}
	if hits := query(t, r, "+tag:alpha"); len(hits) != 5 {
		t.Fatalf("hits=%d after recovery", len(hits))
	}
	r.Close()
}

// A field can exist with no tokens (all its values were empty). A range query
// on it indexed Tokens[0] and panicked, on a perfectly valid index.
func TestRegression_RangeQueryOnEmptyField(t *testing.T) {
	dir := t.TempDir()
	docs := manyDocs("doc", 3, "alpha")
	for _, d := range docs {
		d.Fields = append(d.Fields, &storage.FieldDefinition{Hash: xxh3.HashString("empty")})
	}
	saveIndex(t, filepath.Join(dir, "0.seg"), docs...)
	r := newReader()
	if err := r.Reset(dir); err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for _, q := range []string{"+empty:>a", "+empty:<z", "+empty:>=a +tag:alpha"} {
		func() {
			defer func() {
				if p := recover(); p != nil {
					t.Fatalf("%q panicked: %v", q, p)
				}
			}()
			if hits := query(t, r, q); len(hits) != 0 {
				t.Fatalf("%q: %d hits", q, len(hits))
			}
		}()
	}
}

// BuildFrom used to unmap a loaded storage before reading the new documents,
// which may point into that mapping (rebuilding an index from its own tokens):
// SIGSEGV.
func TestRegression_RebuildFromOwnTokens(t *testing.T) {
	if isChild() {
		var s storage.Storage
		if err := s.Load(os.Getenv("POC_FILE")); err != nil {
			t.Fatal(err)
		}
		field := s.Fields[xxh3.HashString(tagField)]
		var docs []*storage.Document
		for i := range s.DocumentsIds {
			fd := &storage.FieldDefinition{Hash: xxh3.HashString(tagField), Length: 1}
			for j := range field.Tokens {
				// Values alias the mapping that BuildFrom replaces.
				fd.Tokens = append(fd.Tokens, &storage.TokenDefinition{Value: field.Tokens[j].Value.Bytes(), Frequency: 1})
			}
			docs = append(docs, &storage.Document{Id: s.DocumentsIds[i], Fields: []*storage.FieldDefinition{fd}})
		}
		err := s.BuildFrom(docs...)
		fmt.Printf("RESULT err=%v docs=%d verify=%v\n", err, len(s.DocumentsIds), s.Verify())
		return
	}
	file := filepath.Join(t.TempDir(), "0.seg")
	saveIndex(t, file, manyDocs("doc", 50, "alpha", "beta")...)
	out, ee := runChild(t, t.Name(), "POC_FILE="+file)
	requireChildOK(t, out, ee, "RESULT err=<nil> docs=50 verify=<nil>")
}

// A failed BuildFrom must leave the storage as it was.
func TestRegression_FailedBuildFromKeepsStorage(t *testing.T) {
	var s storage.Storage
	if err := s.BuildFrom(manyDocs("doc", 4, "alpha")...); err != nil {
		t.Fatal(err)
	}
	if err := s.BuildFrom(mkDoc("x", strings.Repeat("a", 200))); err == nil {
		t.Fatal("long token accepted")
	}
	if len(s.DocumentsIds) != 4 {
		t.Fatalf("storage changed by a failed BuildFrom: %d docs", len(s.DocumentsIds))
	}
}

// Bytes of a reused DocumentId past its Size used to be written to the file.
func TestRegression_DocumentIdSlotZeroed(t *testing.T) {
	id := storage.DocumentId{Value: storage.RawValueFrom("secret-previous-document-id")}
	id.Value.Size = uint64(copy(id.Value.Data[:], "ab")) // reused buffer, stale tail
	doc := mkDoc("unused", "x")
	doc.Id = id
	file := filepath.Join(t.TempDir(), "0.seg")
	saveIndex(t, file, doc)
	data, _ := os.ReadFile(file)
	if bytes.Contains(data, []byte("previous-document")) {
		t.Fatal("stale id bytes written to the file")
	}
}

// BuildFrom's Size must be the exact size of the saved file.
func TestRegression_SizeMatchesFile(t *testing.T) {
	var s storage.Storage
	if err := s.BuildFrom(manyDocs("doc", 300, "alpha", "beta")...); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "0.seg")
	if err := s.SaveTo(file); err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(file)
	if uint64(info.Size()) != s.Size {
		t.Fatalf("file is %d bytes, Size says %d", info.Size(), s.Size)
	}
	var loaded storage.Storage
	if err := loaded.Load(file); err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	if loaded.Size != s.Size {
		t.Fatalf("loaded Size %d, built %d", loaded.Size, s.Size)
	}
}

// Merging two storages whose shared field has tokens but no lengths computed
// 0/0 and stored a NaN average, making every BM25 score in the field NaN.
func TestRegression_MergeAverageLengthNotNaN(t *testing.T) {
	dir := t.TempDir()
	mk := func(name, prefix string) *storage.Storage {
		docs := manyDocs(prefix, 3, "alpha")
		for _, d := range docs {
			d.Fields[0].Length = 0
		}
		saveIndex(t, filepath.Join(dir, name), docs...)
		var s storage.Storage
		if err := s.Load(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
		return &s
	}
	a, b := mk("a.seg", "a"), mk("b.seg", "b")
	defer a.Close()
	defer b.Close()
	out := filepath.Join(dir, "m.seg")
	if err := (&storage.Merger{TempDir: t.TempDir()}).Merge(out, a, b); err != nil {
		t.Fatal(err)
	}
	var m storage.Storage
	if err := m.Load(out); err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if avg := m.Fields[xxh3.HashString(tagField)].AvgDocumentLength; math.IsNaN(float64(avg)) {
		t.Fatal("merged average document length is NaN")
	}
}

// A crash leaves temporary segment files; the next Merge deletes them and
// nothing else.
func TestRegression_MergeCleansLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	w := &textiplex.Writer{Directory: dir, TemporaryDirectory: t.TempDir()}
	for i := range 2 {
		if err := w.Batch(mkBatch(manyDocs(fmt.Sprintf("s%d", i), 10, "alpha")...)); err != nil {
			t.Fatal(err)
		}
	}
	leftover := filepath.Join(dir, ".0000000000000009.seg.k3x9q2.tmp")
	unrelated := filepath.Join(dir, ".notes.tmp")
	os.WriteFile(leftover, make([]byte, 1<<20), 0o644)
	os.WriteFile(unrelated, []byte("keep"), 0o644)

	if err := w.Merge(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatal("leftover temporary segment not removed")
	}
	if _, err := os.Stat(unrelated); err != nil {
		t.Fatal("unrelated file removed")
	}
	if n, err := countDocs(dir); err != nil || n != 20 {
		t.Fatalf("docs=%d err=%v", n, err)
	}
}
