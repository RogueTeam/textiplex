package storage_test

import (
	"fmt"
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

const benchDocCount = 1_000_000

// prepareBlugeEquivalent builds 1M documents with 3 fields each containing
// 1 unique token — directly equivalent to Bluge's BenchmarkOfflineWriter:
//
//	documents.NewKeywordField("name",          fmt.Sprintf("hello-%d", index))
//	documents.NewKeywordField("index",         fmt.Sprintf("%d", index))
//	documents.NewKeywordField("reversed-name", fmt.Sprintf("olleh-%d", index))
//
// Field hashes are the xxh3 equivalents of those field name strings.
// Prepared entirely outside the benchmark clock.
func prepareBlugeEquivalent() []*storage.Document {
	const (
		fieldName         = uint64(0x1111111111111111) // stand-in for xxh3("name")
		fieldIndex        = uint64(0x2222222222222222) // stand-in for xxh3("index")
		fieldReversedName = uint64(0x3333333333333333) // stand-in for xxh3("reversed-name")
	)

	docs := make([]*storage.Document, benchDocCount)
	for i := range benchDocCount {
		docs[i] = testsuite.MakeDoc(
			fmt.Sprintf("%d", i),
			testsuite.MakeField(fieldName, 1,
				testsuite.MakeToken(fmt.Sprintf("hello-%d", i), 1),
			),
			testsuite.MakeField(fieldIndex, 1,
				testsuite.MakeToken(fmt.Sprintf("%d", i), 1),
			),
			testsuite.MakeField(fieldReversedName, 1,
				testsuite.MakeToken(fmt.Sprintf("olleh-%d", i), 1),
			),
		)
	}
	return docs
}

// BenchmarkBuildFromSorted is the apples-to-apples equivalent of
// Bluge's BenchmarkOfflineWriter. Batch preparation happens outside
// the clock; only BuildFromSorted is measured per iteration.
func BenchmarkBuildFromSorted(b *testing.B) {
	docs := prepareBlugeEquivalent()
	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		var s storage.Storage
		s.BuildFromSorted(docs...)
	}
}

// BenchmarkLoadBytes measures LoadBytes on a mmap'd file built from the
// same 1M-document corpus. Build and serialization happen outside the clock.
func BenchmarkLoadBytes(b *testing.B) {
	assertions := assert.New(b)

	docs := prepareBlugeEquivalent()

	var s storage.Storage
	s.BuildFromSorted(docs...)

	filename := testsuite.TempFilename(b, "storage_bench_*.bin")

	err := s.SaveTo(filename)
	if !assertions.Nil(err, "failed to save storage into file") {
		return
	}
	b.Cleanup(func() {
		s.Close()
	})

	b.ReportAllocs()
	b.SetBytes(int64(s.Size))
	b.ResetTimer()

	for b.Loop() {
		var loaded storage.Storage
		err := loaded.Load(filename)
		if !assertions.Nil(err, "failed to load bytes") {
			return
		}
		loaded.Close()
	}
}
