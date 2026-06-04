package fields_test

import (
	"fmt"
	"testing"

	"github.com/RogueTeam/textiplex/fields"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
)

const benchDocCount = 1_000_000

// BenchmarkDocumentConstruction measures the full cost of building 1M
// storage-ready documents from raw field values — field hashing, token
// encoding, pool allocation, and batch insertion — mirroring the corpus
// shape used by BenchmarkBuildFromSorted:
//
//   - "name"          keyword field, unique per doc ("hello-N")
//   - "index"         integer field, unique per doc (N)
//   - "reversed-name" keyword field, unique per doc ("olleh-N")
//
// Nothing is outside the clock except the pool construction, which is
// a one-time fixed cost and not part of per-document work.
func BenchmarkDocumentConstruction(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		tokPool := pool.New[storage.TokenDefinition](5)
		fieldPool := pool.New[storage.FieldDefinition](3)
		batch := fields.NewBatch(benchDocCount)

		var idBuf []byte
		var nameBuf []byte
		var reversedBuf []byte
		fieldsPtrs := make([]*storage.FieldDefinition, 0, 3)

		for i := range benchDocCount {
			fieldsPtrs = fieldsPtrs[:0]

			nameField := fieldPool.Get()
			nameBuf = fmt.Appendf(nameBuf[:0], "hello-%d", i)
			totalSize := fields.KeywordField(nameField, tokPool, "name", nameBuf)
			fieldsPtrs = append(fieldsPtrs, nameField)

			indexField := fieldPool.Get()
			totalSize += fields.IntegerField(indexField, tokPool, "index", i)
			fieldsPtrs = append(fieldsPtrs, indexField)

			reversedField := fieldPool.Get()
			reversedBuf = fmt.Appendf(reversedBuf[:0], "olleh-%d", i)
			totalSize += fields.KeywordField(reversedField, tokPool, "reversed-name", reversedBuf)
			fieldsPtrs = append(fieldsPtrs, reversedField)

			idBuf = fmt.Appendf(idBuf[:0], "%d", i)
			batch.Insert(idBuf, totalSize, fieldsPtrs...)
		}
	}
}

// BenchmarkDocumentConstructionAndBuild measures the end-to-end pipeline:
// document construction followed immediately by BuildFromSorted. This is
// the full ingestion cost for a single 1M-doc segment — what the IndexWriter
// pays per flush on the hot path.
func BenchmarkDocumentConstructionAndBuild(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		tokPool := pool.New[storage.TokenDefinition](5)
		fieldPool := pool.New[storage.FieldDefinition](3)
		batch := fields.NewBatch(benchDocCount)

		var idBuf []byte
		var nameBuf []byte
		var reversedBuf []byte
		fieldsPtrs := make([]*storage.FieldDefinition, 0, 3)

		for i := range benchDocCount {
			fieldsPtrs = fieldsPtrs[:0]

			nameField := fieldPool.Get()
			nameBuf = fmt.Appendf(nameBuf[:0], "hello-%d", i)
			totalSize := fields.KeywordField(nameField, tokPool, "name", nameBuf)
			fieldsPtrs = append(fieldsPtrs, nameField)

			indexField := fieldPool.Get()
			totalSize += fields.IntegerField(indexField, tokPool, "index", i)
			fieldsPtrs = append(fieldsPtrs, indexField)

			reversedField := fieldPool.Get()
			reversedBuf = fmt.Appendf(reversedBuf[:0], "olleh-%d", i)
			totalSize += fields.KeywordField(reversedField, tokPool, "reversed-name", reversedBuf)
			fieldsPtrs = append(fieldsPtrs, reversedField)

			idBuf = fmt.Appendf(idBuf[:0], "%d", i)
			batch.Insert(idBuf, totalSize, fieldsPtrs...)
		}

		var s storage.Storage
		s.BuildFromSorted(batch.Documents...)
	}
}

// BenchmarkDocumentConstructionOnly isolates construction from build so the
// two costs can be compared independently. Identical to
// BenchmarkDocumentConstruction but makes the intent explicit via the name
// and reports bytes based on the estimated raw input size.
func BenchmarkDocumentConstructionOnly(b *testing.B) {
	// Estimate: avg doc id 5B + 3 field names ~15B + token values ~15B = ~35B raw input per doc
	b.SetBytes(int64(benchDocCount) * 35)
	b.ReportAllocs()

	for b.Loop() {
		tokPool := pool.New[storage.TokenDefinition](5)
		fieldPool := pool.New[storage.FieldDefinition](3)
		batch := fields.NewBatch(benchDocCount)

		var idBuf []byte
		var nameBuf []byte
		var reversedBuf []byte
		fieldsPtrs := make([]*storage.FieldDefinition, 0, 3)

		for i := range benchDocCount {
			fieldsPtrs = fieldsPtrs[:0]

			nameField := fieldPool.Get()
			nameBuf = fmt.Appendf(nameBuf[:0], "hello-%d", i)
			totalSize := fields.KeywordField(nameField, tokPool, "name", nameBuf)
			fieldsPtrs = append(fieldsPtrs, nameField)

			indexField := fieldPool.Get()
			totalSize += fields.IntegerField(indexField, tokPool, "index", i)
			fieldsPtrs = append(fieldsPtrs, indexField)

			reversedField := fieldPool.Get()
			reversedBuf = fmt.Appendf(reversedBuf[:0], "olleh-%d", i)
			totalSize += fields.KeywordField(reversedField, tokPool, "reversed-name", reversedBuf)
			fieldsPtrs = append(fieldsPtrs, reversedField)

			idBuf = fmt.Appendf(idBuf[:0], "%d", i)
			batch.Insert(idBuf, totalSize, fieldsPtrs...)
		}

		_ = batch
	}
}

// prepareBlugeEquivalentFromFields builds the same 1M-doc corpus as
// prepareBlugeEquivalent in the storage benchmark but via the fields
// package. Used to verify the fields package produces storage-compatible
// output and to provide a direct apples-to-apples comparison point.
func prepareBlugeEquivalentFromFields(b *testing.B) []*storage.Document {
	b.Helper()

	tokPool := pool.New[storage.TokenDefinition](5)
	fieldPool := pool.New[storage.FieldDefinition](3)
	batch := fields.NewBatch(benchDocCount)

	var idBuf []byte
	var nameBuf []byte
	var reversedBuf []byte
	fieldsPtrs := make([]*storage.FieldDefinition, 0, 3)

	for i := range benchDocCount {
		fieldsPtrs = fieldsPtrs[:0]

		nameField := fieldPool.Get()
		nameBuf = fmt.Appendf(nameBuf[:0], "hello-%d", i)
		totalSize := fields.KeywordField(nameField, tokPool, "name", nameBuf)
		fieldsPtrs = append(fieldsPtrs, nameField)

		indexField := fieldPool.Get()
		totalSize += fields.IntegerField(indexField, tokPool, "index", i)
		fieldsPtrs = append(fieldsPtrs, indexField)

		reversedField := fieldPool.Get()
		reversedBuf = fmt.Appendf(reversedBuf[:0], "olleh-%d", i)
		totalSize += fields.KeywordField(reversedField, tokPool, "reversed-name", reversedBuf)
		fieldsPtrs = append(fieldsPtrs, reversedField)

		idBuf = fmt.Appendf(idBuf[:0], "%d", i)
		batch.Insert(idBuf, totalSize, fieldsPtrs...)
	}

	return batch.Documents
}

// BenchmarkBuildFromSortedViaFields is the direct equivalent of
// BenchmarkBuildFromSorted in the storage package but sourced through the
// fields package. Measures only BuildFromSorted with docs prepared outside
// the clock, allowing an apples-to-apples comparison between raw testsuite
// docs and fields-package-constructed docs.
func BenchmarkBuildFromSortedViaFields(b *testing.B) {
	docs := prepareBlugeEquivalentFromFields(b)

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		var s storage.Storage
		s.BuildFromSorted(docs...)
		_ = s
	}
}

// BenchmarkFieldsVsTestsuite compares construction cost between the fields
// package and the raw testsuite helpers. Run with -v to see per-benchmark
// times side by side.
func BenchmarkFieldsVsTestsuite(b *testing.B) {
	b.ReportAllocs()
	b.Run("via_fields_package", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			tokPool := pool.New[storage.TokenDefinition](5)
			fieldPool := pool.New[storage.FieldDefinition](3)
			batch := fields.NewBatch(benchDocCount)

			var idBuf []byte
			var nameBuf []byte
			var reversedBuf []byte
			fieldsPtrs := make([]*storage.FieldDefinition, 0, 3)

			for i := range benchDocCount {
				fieldsPtrs = fieldsPtrs[:0]

				nameField := fieldPool.Get()
				nameBuf = fmt.Appendf(nameBuf[:0], "hello-%d", i)
				totalSize := fields.KeywordField(nameField, tokPool, "name", nameBuf)
				fieldsPtrs = append(fieldsPtrs, nameField)

				indexField := fieldPool.Get()
				totalSize += fields.IntegerField(indexField, tokPool, "index", i)
				fieldsPtrs = append(fieldsPtrs, indexField)

				reversedField := fieldPool.Get()
				reversedBuf = fmt.Appendf(reversedBuf[:0], "olleh-%d", i)
				totalSize += fields.KeywordField(reversedField, tokPool, "reversed-name", reversedBuf)
				fieldsPtrs = append(fieldsPtrs, reversedField)

				idBuf = fmt.Appendf(idBuf[:0], "%d", i)
				batch.Insert(idBuf, totalSize, fieldsPtrs...)
			}
		}
	})

	b.Run("via_testsuite_helpers", func(b *testing.B) {
		const (
			fieldName         = uint64(0x1111111111111111)
			fieldIndex        = uint64(0x2222222222222222)
			fieldReversedName = uint64(0x3333333333333333)
		)
		b.ReportAllocs()
		for b.Loop() {
			docs := make([]*storage.Document, 0, benchDocCount)
			for i := range benchDocCount {
				doc := testsuite.MakeDoc(
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
				docs = append(docs, doc)
			}
			_ = docs
		}
	})
}
