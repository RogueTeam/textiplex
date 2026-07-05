package storage_test

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/RogueTeam/textiplex/storage"
)

// Many random seeds; also asserts SaveTo's Size accounting matches the file
// size exactly (an over-count leaves garbage tail; an under-count silently
// drops data because append() escapes the mmap).
func TestAudit_Stress_ManySeeds(t *testing.T) {
	dir := t.TempDir()
	fieldsA := []string{"shared1", "shared2", "onlyA"}
	fieldsB := []string{"shared1", "shared2", "onlyB"}
	for seed := int64(100); seed < 130; seed++ {
		r := rand.New(rand.NewSource(seed))
		vocab := make([]string, 5+r.Intn(150))
		for i := range vocab {
			vocab[i] = fmt.Sprintf("s%d_%03d", seed%7, i)
		}
		docsA := genDocs(r, fmt.Sprintf("A%d", seed), 1+r.Intn(200), fieldsA, vocab)
		docsB := genDocs(r, fmt.Sprintf("B%d", seed), 1+r.Intn(200), fieldsB, vocab)
		trA, trB := newTruth(), newTruth()
		trA.addDocs(docsA)
		trB.addDocs(docsB)

		var bA storage.Storage
		bA.BuildFrom(docsA...)
		pathA := filepath.Join(dir, fmt.Sprintf("a%d.idx", seed))
		if err := bA.SaveTo(pathA); err != nil {
			t.Fatalf("seed %d SaveTo A: %v", seed, err)
		}
		if info, _ := os.Stat(pathA); uint64(info.Size()) != bA.Size {
			t.Fatalf("seed %d: Size accounting mismatch: file %d vs computed %d", seed, info.Size(), bA.Size)
		}

		var a storage.Storage
		if err := a.Load(pathA); err != nil {
			t.Fatalf("seed %d Load A: %v", seed, err)
		}
		if a.Size != bA.Size {
			t.Fatalf("seed %d: loaded Size %d != built Size %d (trailing bytes unread)", seed, a.Size, bA.Size)
		}
		verify(t, fmt.Sprintf("stress-A-%d", seed), &a, trA)

		var bB storage.Storage
		bB.BuildFrom(docsB...)
		pathB := filepath.Join(dir, fmt.Sprintf("b%d.idx", seed))
		if err := bB.SaveTo(pathB); err != nil {
			t.Fatalf("seed %d SaveTo B: %v", seed, err)
		}
		var b storage.Storage
		if err := b.Load(pathB); err != nil {
			t.Fatalf("seed %d Load B: %v", seed, err)
		}

		m := storage.Merger{TempDir: dir}
		mp := filepath.Join(dir, fmt.Sprintf("m%d.idx", seed))
		if err := m.Merge(mp, &a, &b); err != nil {
			t.Fatalf("seed %d Merge: %v", seed, err)
		}
		var s storage.Storage
		if err := s.Load(mp); err != nil {
			t.Fatalf("seed %d Load merged: %v", seed, err)
		}
		if info, _ := os.Stat(mp); uint64(info.Size()) != s.Size {
			t.Fatalf("seed %d: merged file has %d bytes but Load consumed %d (unaccounted tail)", seed, info.Size(), s.Size)
		}
		verify(t, fmt.Sprintf("stress-merged-%d", seed), &s, trA.merge(trB))
		s.Close()
		a.Close()
		b.Close()
		os.Remove(pathA)
		os.Remove(pathB)
		os.Remove(mp)
	}
}
