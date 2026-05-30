package storage_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"golang.org/x/sys/unix"
)

const (
	benchDocCount   = 1_000_000
	benchFieldCount = 4
	benchTokenCount = 10
)

// prepareDocs builds benchDocCount documents outside the benchmark clock.
// Each document has benchFieldCount fields, each field has benchTokenCount
// unique tokens with frequency 1. Field hashes and token values are
// deterministic so repeated runs are comparable.
func prepareDocs() []*storage.Document {
	docs := make([]*storage.Document, benchDocCount)

	// vocabulary per field — simulate realistic token variety
	vocab := make([][]string, benchFieldCount)
	for f := range benchFieldCount {
		vocab[f] = make([]string, 1000) // 1000 unique tokens per field
		for v := range 1000 {
			vocab[f][v] = fmt.Sprintf("f%d_token%d", f, v)
		}
	}

	for i := range benchDocCount {
		fields := make([]*storage.FieldDefinition, benchFieldCount)
		for f := range benchFieldCount {
			// each doc gets 10 tokens sampled from the vocabulary
			tokens := make([]*storage.TokenDefinition, benchTokenCount)
			base := (i * benchTokenCount) % (len(vocab[f]) - benchTokenCount)
			for tk := range benchTokenCount {
				tokens[tk] = testsuite.MakeToken(vocab[f][base+tk], 1)
			}
			fields[f] = testsuite.MakeField(uint64(f+1), uint64(benchTokenCount), tokens...)
		}
		docs[i] = testsuite.MakeDoc(fmt.Sprintf("CO1.PCCNTR.%08d", i), fields...)
	}

	return docs
}

var sampleData = prepareDocs()

// BenchmarkBuildFromSorted measures the time to instantiate a Storage and
// call BuildFromSorted over 10M pre-sorted documents.
// Preparation of the document slice happens outside the benchmark clock.
func BenchmarkBuildFromSorted(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		var s storage.Storage
		s.BuildFromSorted(sampleData...)
	}
}

// BenchmarkLoadBytes measures how fast LoadBytes can map and parse a fully
// built index. Building and serialising the index happens outside the clock.
// Each iteration loads from a mmap'd file so it exercises the real read path
// including OS page faults on the first iteration.
func BenchmarkLoadBytes(b *testing.B) {
	// Build the index once outside the clock.
	var s storage.Storage
	s.BuildFromSorted(sampleData...)

	// Serialise to a temp file via mmap so LoadBytes exercises the real path.
	buf := s.Save(nil)

	f, err := os.CreateTemp("", "storage_bench_*.bin")
	if err != nil {
		b.Fatalf("create temp file: %v", err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if err := f.Truncate(int64(len(buf))); err != nil {
		b.Fatalf("truncate: %v", err)
	}
	mapped, err := unix.Mmap(
		int(f.Fd()),
		0,
		len(buf),
		unix.PROT_READ|unix.PROT_WRITE,
		unix.MAP_SHARED,
	)
	if err != nil {
		b.Fatalf("mmap: %v", err)
	}
	copy(mapped, buf)
	if err := unix.Msync(mapped, unix.MS_SYNC); err != nil {
		b.Fatalf("msync: %v", err)
	}
	// Remap read-only for the actual benchmark — mirrors production use.
	unix.Munmap(mapped)
	readOnly, err := unix.Mmap(
		int(f.Fd()),
		0,
		len(buf),
		unix.PROT_READ,
		unix.MAP_SHARED,
	)
	if err != nil {
		b.Fatalf("mmap read-only: %v", err)
	}
	defer unix.Munmap(readOnly)

	b.ReportAllocs()
	b.SetBytes(int64(len(readOnly)))

	b.ResetTimer()
	for b.Loop() {
		var loaded storage.Storage
		if err := loaded.LoadBytes(readOnly); err != nil {
			b.Fatalf("LoadBytes: %v", err)
		}
	}
}
