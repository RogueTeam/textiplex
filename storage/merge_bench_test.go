package storage_test

import (
	"fmt"
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

const benchMergeHalf = benchDocCount / 2 // 500_000 per side -> 1M merged

// prepareMergeHalf builds `count` documents starting at `start`, mirroring the
// Bluge-equivalent shape used by BenchmarkBuildFromSorted: 3 fields, one unique
// token per field. Doc IDs are zero padded so lexicographic order matches the
// numeric order, which keeps the two halves on disjoint ordered ranges (every
// id in b sorts after every id in a) as Merge requires.
//
// The three field hashes are identical across both halves, so every field is a
// collision field at merge time. Token values are unique per document, so each
// collision field merges via the disjoint-token path (no shared-token unions),
// which is the realistic FTS case: same schema, disjoint vocab per shard.
func prepareMergeHalf(start, count int) (batch *storage.Batch) {
	const (
		fieldName         = uint64(0x1111111111111111)
		fieldIndex        = uint64(0x2222222222222222)
		fieldReversedName = uint64(0x3333333333333333)
	)

	batch = storage.NewBatch()
	batch.Documents = make([]*storage.Document, 0, count)

	for i := range count {
		n := start + i

		batch.Insert(testsuite.MakeDoc(
			// 12-digit zero pad: lexicographic == numeric for the full 1M range.
			fmt.Sprintf("%012d", n),
			testsuite.MakeField(fieldName, 1,
				testsuite.MakeToken(fmt.Sprintf("hello-%d", n), 1),
			),
			testsuite.MakeField(fieldIndex, 1,
				testsuite.MakeToken(fmt.Sprintf("%d", n), 1),
			),
			testsuite.MakeField(fieldReversedName, 1,
				testsuite.MakeToken(fmt.Sprintf("olleh-%d", n), 1),
			),
		))
	}
	return batch
}

// BenchmarkMerge is the merge analogue of BenchmarkBuildFromSorted. Both halves
// are built and saved to disk outside the clock; only Merger.Merge — the
// streaming, temp-file-backed merge that produces the final 1M-doc file — is
// measured per iteration.
func BenchmarkMerge(b *testing.B) {
	assertions := assert.New(b)

	// Build both halves on disjoint ordered ranges.
	var aStore storage.Storage
	aStore.BuildFromSorted(prepareMergeHalf(0, benchMergeHalf))

	var bStore storage.Storage
	bStore.BuildFromSorted(prepareMergeHalf(benchMergeHalf, benchMergeHalf))

	// Persist them and load via mmap so the benchmark merges from on-disk,
	// zero-copy storages — the production path, where posting lists are Unsafe
	// and backed by mmap pages rather than heap bitmaps.
	aFile := testsuite.TempFilename(b, "merge_bench_a_*.bin")
	bFile := testsuite.TempFilename(b, "merge_bench_b_*.bin")

	if !assertions.Nil(aStore.SaveTo(aFile), "failed to save a") {
		return
	}
	if !assertions.Nil(bStore.SaveTo(bFile), "failed to save b") {
		return
	}

	var a storage.Storage
	if !assertions.Nil(a.Load(aFile), "failed to load a") {
		return
	}
	b.Cleanup(func() { a.Close() })

	var bb storage.Storage
	if !assertions.Nil(bb.Load(bFile), "failed to load b") {
		return
	}
	b.Cleanup(func() { bb.Close() })

	m := storage.Merger{TempDir: b.TempDir()}
	out := testsuite.TempFilename(b, "merge_bench_out_*.bin")

	totalDocs := int64(len(a.DocumentsIds) + len(bb.DocumentsIds))

	b.ReportAllocs()
	b.SetBytes(int64(a.Size + bb.Size))
	b.ResetTimer()

	for b.Loop() {
		err := m.Merge(out, &a, &bb)
		if !assertions.Nil(err, "merge failed") {
			return
		}
	}
	b.StopTimer()

	// Sanity check outside the clock: the merged file loads and has every doc.
	var merged storage.Storage
	if assertions.Nil(merged.Load(out), "merged file must load") {
		assertions.Equal(totalDocs, int64(len(merged.DocumentsIds)), "merged doc count")
		merged.Close()
	}
}
