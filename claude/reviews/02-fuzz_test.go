package reviews_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/RogueTeam/textiplex"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/zeebo/xxh3"
)

// fuzzSeedIndex is a small but varied index: two fields, shared and unique
// tokens, frequencies > 1, a document missing a field, and an empty field.
func fuzzSeedIndex(tb testing.TB) []byte {
	tb.Helper()
	var docs []*storage.Document
	for i := range 12 {
		tag := &storage.FieldDefinition{Hash: xxh3.HashString(tagField), Length: 3, Tokens: []*storage.TokenDefinition{
			{Value: []byte("alpha"), Frequency: uint32(1 + i%3)},
			{Value: []byte(fmt.Sprintf("uniq%02d", i)), Frequency: 1},
		}}
		if i%2 == 0 {
			tag.Tokens = append(tag.Tokens, &storage.TokenDefinition{Value: []byte("beta"), Frequency: 2})
		}
		body := &storage.FieldDefinition{Hash: xxh3.HashString("body"), Length: 2, Tokens: []*storage.TokenDefinition{
			{Value: []byte("gamma"), Frequency: 1},
			{Value: []byte(fmt.Sprintf("b%d", i%4)), Frequency: 1},
		}}
		empty := &storage.FieldDefinition{Hash: xxh3.HashString("empty")}
		fields := []*storage.FieldDefinition{tag, empty}
		if i != 5 {
			fields = append(fields, body)
		}
		docs = append(docs, &storage.Document{Id: storage.DocumentId{Value: storage.RawValueFrom(fmt.Sprintf("doc-%02d", i))}, Fields: fields})
	}
	path := filepath.Join(tb.TempDir(), "seed.seg")
	saveIndex(tb, path, docs...)
	data, err := os.ReadFile(path)
	if err != nil {
		tb.Fatal(err)
	}
	return data
}

var fuzzQueries = []string{
	"+tag:alpha",
	"tag:alpha tag:beta",
	"+tag:alpha -tag:beta",
	"+tag:alpha +body:gamma -body:b1",
	"+tag:alph~1",
	"tag:uniq03 body:b2;2",
	"+tag:>=beta",
	"+tag:<uniq05",
	"+body:>a",
	"+empty:>a",
	"+empty:<z",
	"+missing:x",
}

// exerciseIndex runs every read path over the index in dir and fails on panic.
func exerciseIndex(t *testing.T, dir string, good *storage.Storage) {
	path := filepath.Join(dir, "0.seg")
	var s storage.Storage
	if err := s.Load(path); err != nil {
		return
	}
	defer s.Close()
	_ = s.Verify()

	r := newReader()
	if err := r.Reset(dir); err != nil {
		return
	}
	defer r.Close()
	r.Searcher.LevenshteinMaxK = 2
	r.Searcher.LevenshteinM = 50
	for _, q := range fuzzQueries {
		for _, sortField := range []textiplex.SortField{textiplex.SortFieldBM25, textiplex.SortField(xxh3.HashString("body"))} {
			for _, reverse := range []bool{false, true} {
				seq, err := r.QueryString(1, sortField, reverse, q)
				if err != nil {
					continue
				}
				for range seq {
				}
			}
		}
	}

	out := t.TempDir()
	_ = (&storage.Merger{TempDir: out}).Merge(filepath.Join(out, "m1.seg"), &s, good)
	_ = (&storage.Merger{TempDir: out}).Merge(filepath.Join(out, "m2.seg"), good, &s)
	_ = s.SaveTo(filepath.Join(out, "resaved.seg"))
}

// FuzzCorruptIndex mutates a valid index file (both format versions) and runs
// every read path over it. A damaged index may be rejected or return wrong
// results, but must never panic or crash the process.
//
//	go test -run=^$ -fuzz=FuzzCorruptIndex -fuzztime=2m ./claude/reviews/
func FuzzCorruptIndex(f *testing.F) {
	seed := fuzzSeedIndex(f)
	f.Add(seed)
	f.Add(toV1(seed))

	goodPath := filepath.Join(f.TempDir(), "good.seg")
	if err := os.WriteFile(goodPath, seed, 0o600); err != nil {
		f.Fatal(err)
	}
	var good storage.Storage
	if err := good.Load(goodPath); err != nil {
		f.Fatal(err)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "0.seg"), data, 0o600); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		exerciseIndex(t, dir, &good)
		if elapsed := time.Since(start); elapsed > 5*time.Second {
			t.Fatalf("a %d byte index took %v: a crafted file must not stall the reader", len(data), elapsed)
		}
	})
}

// The unmutated seed must pass every path cleanly, including Verify.
func TestFuzzSeedIsValid(t *testing.T) {
	data := fuzzSeedIndex(t)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "0.seg"), data, 0o600)
	var s storage.Storage
	if err := s.Load(filepath.Join(dir, "0.seg")); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(); err != nil {
		t.Fatal(err)
	}
	exerciseIndex(t, dir, &s)
	s.Close()
}
