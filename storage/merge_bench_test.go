package storage_test

import (
	"fmt"
	"os"
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
	"golang.org/x/sys/unix"
)

// SECOP-realistic corpus constants.
//
// Each half has 500K documents (1M total across both storages).
// 4 fields model real SECOP contract fields:
//   - descripcion_objeto: long text, large shared vocabulary, high avgdl
//   - nombre_contratista: proper nouns, medium vocabulary, mostly unique per doc
//   - entidad_nombre:     entity names, small vocabulary, high repetition
//   - estado_contrato:    enum-like, tiny vocabulary, appears in every doc
//
// Token distribution is power-law: a small set of tokens appears in many
// documents, most tokens appear in very few. This is realistic for Spanish
// procurement text and produces the mixed shared/unique token scenario that
// exercises all three paths in the merge algorithm (only-in-a, only-in-b,
// both).
const (
	benchHalfCount = 500_000 // docs per storage — 1M total

	fieldDescripcion = uint64(0x1111111111111111)
	fieldContratista = uint64(0x2222222222222222)
	fieldEntidad     = uint64(0x3333333333333333)
	fieldEstado      = uint64(0x4444444444444444)

	// vocabulary sizes per field — determines token sharing between a and b
	vocabDescripcion = 50_000  // large, ~50% shared between a and b
	vocabContratista = 200_000 // mostly unique proper nouns
	vocabEntidad     = 500     // small, highly repeated, mostly shared
	vocabEstado      = 8       // enum: EJECUTADO, LIQUIDADO, TERMINADO, etc.

	// tokens per doc per field — approximates real field lengths
	tokensDescripcion = 12
	tokensContratista = 3
	tokensEntidad     = 2
	tokensEstado      = 1
)

// prepareRealisticHalf builds 500K documents for one storage half.
// docOffset shifts doc IDs so a's IDs all sort before b's IDs:
//   - half a: docOffset=0        → IDs "a-00000000" … "a-00499999"
//   - half b: docOffset=500_000  → IDs "b-00000000" … "b-00499999"
//
// The "a-" / "b-" prefix guarantees a's IDs sort before b's alphabetically,
// satisfying the disjoint ordered ranges invariant required by Merge.
//
// vocabOffset shifts the token vocabulary so a and b share roughly half their
// tokens in descripcion and entidad fields, while contratista tokens are
// mostly unique.
func prepareRealisticHalf(prefix string, vocabOffset int) []*storage.Document {
	docs := make([]*storage.Document, benchHalfCount)

	for i := range benchHalfCount {
		docID := fmt.Sprintf("%s-%08d", prefix, i)

		// descripcion_objeto — 12 tokens from large vocabulary
		// power-law: first 5000 tokens are common (shared), rest are rare
		descTokens := make([]*storage.TokenDefinition, tokensDescripcion)
		for t := range tokensDescripcion {
			var vocabIdx int
			if t < 3 {
				// common tokens: low indices shared across both halves
				vocabIdx = (i*tokensDescripcion + t) % 5000
			} else {
				// rare tokens: offset into each half's unique range
				vocabIdx = vocabOffset + (i*tokensDescripcion+t)%vocabDescripcion
			}
			freq := uint64(1 + t%4) // 1-4 occurrences, realistic for body text
			descTokens[t] = testsuite.MakeToken(fmt.Sprintf("desc_%05d", vocabIdx), freq)
		}

		// nombre_contratista — 3 tokens, mostly unique per doc
		contraTokens := make([]*storage.TokenDefinition, tokensContratista)
		for t := range tokensContratista {
			vocabIdx := vocabOffset + (i*tokensContratista+t)%vocabContratista
			contraTokens[t] = testsuite.MakeToken(fmt.Sprintf("contr_%06d", vocabIdx), 1)
		}

		// entidad_nombre — 2 tokens from small vocabulary, high repetition
		// most tokens shared between a and b since vocabulary is tiny
		entTokens := make([]*storage.TokenDefinition, tokensEntidad)
		for t := range tokensEntidad {
			vocabIdx := (i*tokensEntidad + t) % vocabEntidad
			entTokens[t] = testsuite.MakeToken(fmt.Sprintf("ent_%03d", vocabIdx), 1)
		}

		// estado_contrato — 1 token from 8-value enum, shared across all docs
		estadoIdx := i % vocabEstado
		estadoTokens := []*storage.TokenDefinition{
			testsuite.MakeToken(fmt.Sprintf("estado_%d", estadoIdx), 1),
		}

		docs[i] = testsuite.MakeDoc(docID,
			testsuite.MakeField(fieldDescripcion, uint64(tokensDescripcion), descTokens...),
			testsuite.MakeField(fieldContratista, uint64(tokensContratista), contraTokens...),
			testsuite.MakeField(fieldEntidad, uint64(tokensEntidad), entTokens...),
			testsuite.MakeField(fieldEstado, uint64(tokensEstado), estadoTokens...),
		)
	}

	return docs
}

// mmapStorage builds a storage from docs, writes it to a temp mmap'd file,
// and returns a read-only Storage loaded from that file.
// The temp file is cleaned up via b.Cleanup.
func mmapStorage(b *testing.B, assertions *assert.Assertions, docs []*storage.Document) *storage.Storage {
	b.Helper()

	var s storage.Storage
	s.BuildFromSorted(docs...)

	f, err := os.CreateTemp(b.TempDir(), "storage_merge_bench_*.bin")
	if !assertions.Nil(err, "create temp file") {
		return nil
	}
	b.Cleanup(func() {
		f.Close()
		os.Remove(f.Name())
	})

	if !assertions.Nil(f.Truncate(int64(s.Size)), "truncate") {
		return nil
	}

	rw, err := unix.Mmap(int(f.Fd()), 0, int(s.Size),
		unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if !assertions.Nil(err, "mmap rw") {
		return nil
	}

	buf := s.Save(rw[:0])
	if !assertions.Nil(unix.Msync(rw, unix.MS_SYNC), "msync") {
		return nil
	}
	if !assertions.Equal(s.Size, uint64(len(buf)), "size mismatch after save") {
		return nil
	}
	unix.Munmap(rw)

	ro, err := unix.Mmap(int(f.Fd()), 0, int(s.Size),
		unix.PROT_READ, unix.MAP_SHARED)
	if !assertions.Nil(err, "mmap ro") {
		return nil
	}
	b.Cleanup(func() { unix.Munmap(ro) })

	loaded := &storage.Storage{}
	if !assertions.Nil(loaded.LoadBytes(ro), "load bytes") {
		return nil
	}

	return loaded
}

// BenchmarkMerge measures the time to merge two 500K-document mmap'd storages
// into a third mmap'd file. Both source storages and the output file are
// prepared outside the benchmark clock. Only CalculateMergeSize + Truncate +
// mmap + Merge + Msync are measured per iteration — the complete production
// merge path.
func BenchmarkMerge(b *testing.B) {
	assertions := assert.New(b)

	// prepare outside the clock — realistic corpus with ~50% shared vocabulary
	docsA := prepareRealisticHalf("a", 0)
	docsB := prepareRealisticHalf("b", vocabDescripcion/2)

	storageA := mmapStorage(b, assertions, docsA)
	if storageA == nil {
		return
	}
	storageB := mmapStorage(b, assertions, docsB)
	if storageB == nil {
		return
	}

	// pre-compute merge size once to confirm correctness before the loop
	preSize := storage.CalculateMergeSize(storageA, storageB)

	// prepare output file — reused across iterations by re-truncating
	outF, err := os.CreateTemp(b.TempDir(), "storage_merged_*.bin")
	if !assertions.Nil(err, "create output temp file") {
		return
	}
	b.Cleanup(func() {
		outF.Close()
		os.Remove(outF.Name())
	})

	b.ReportAllocs()
	b.SetBytes(int64(preSize))
	b.ResetTimer()

	for b.Loop() {
		b.StopTimer()

		// re-truncate output file for each iteration
		if !assertions.Nil(outF.Truncate(int64(preSize)), "truncate output") {
			return
		}
		outMapped, err := unix.Mmap(int(outF.Fd()), 0, int(preSize),
			unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
		if !assertions.Nil(err, "mmap output") {
			return
		}

		b.StartTimer()

		// timed: size calculation + write + sync
		size := storage.CalculateMergeSize(storageA, storageB)
		out := storage.Merge(outMapped[:0], storageA, storageB)
		unix.Msync(outMapped, unix.MS_SYNC)

		b.StopTimer()

		assertions.Equal(preSize, size, "merge size must be stable across iterations")
		assertions.Equal(preSize, uint64(len(out)), "merge output must match computed size")
		unix.Munmap(outMapped)

		b.StartTimer()
	}
}

// BenchmarkCalculateMergeSize isolates the cost of CalculateMergeSize alone —
// useful for understanding how much of BenchmarkMerge is size calculation
// vs actual data movement.
func BenchmarkCalculateMergeSize(b *testing.B) {
	assertions := assert.New(b)

	docsA := prepareRealisticHalf("a", 0)
	docsB := prepareRealisticHalf("b", vocabDescripcion/2)

	storageA := mmapStorage(b, assertions, docsA)
	if storageA == nil {
		return
	}
	storageB := mmapStorage(b, assertions, docsB)
	if storageB == nil {
		return
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		storage.CalculateMergeSize(storageA, storageB)
	}
}
