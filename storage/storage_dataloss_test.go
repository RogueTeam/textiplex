package storage_test

// Independent data-loss audit for BuildFrom, SaveTo/Load and Merger.Merge.
// A ground-truth model is built directly from the input Documents, and every
// loaded/merged storage is checked exhaustively against it.

import (
	"fmt"
	"math"
	"math/rand"
	"path/filepath"
	"testing"

	"github.com/RoaringBitmap/roaring"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/zeebo/xxh3"
)

// ---------- ground truth model ----------

type truthToken struct {
	freqs map[string]uint32 // docId -> frequency
}

type truthField struct {
	tokens     map[string]*truthToken // token value -> data
	docLengths map[string]uint32      // docId -> length (only > 0)
	totalLen   uint64
	totalFreqs uint64 // number of (token, doc) frequency entries
}

type truth struct {
	docs   []string // insertion order of doc ids
	fields map[uint64]*truthField
}

func newTruth() *truth { return &truth{fields: map[uint64]*truthField{}} }

func (t *truth) addDocs(docs []*storage.Document) {
	for _, doc := range docs {
		id := string(doc.Id.Value.Bytes())
		t.docs = append(t.docs, id)
		for _, f := range doc.Fields {
			tf, ok := t.fields[f.Hash]
			if !ok {
				tf = &truthField{tokens: map[string]*truthToken{}, docLengths: map[string]uint32{}}
				t.fields[f.Hash] = tf
			}
			if f.Length > 0 {
				tf.docLengths[id] = f.Length
				tf.totalLen += uint64(f.Length)
			}
			for _, tok := range f.Tokens {
				tt, ok := tf.tokens[string(tok.Value)]
				if !ok {
					tt = &truthToken{freqs: map[string]uint32{}}
					tf.tokens[string(tok.Value)] = tt
				}
				tt.freqs[id] = tok.Frequency
				tf.totalFreqs++
			}
		}
	}
}

func (t *truth) merge(o *truth) *truth {
	out := newTruth()
	out.docs = append(append([]string{}, t.docs...), o.docs...)
	for _, src := range []*truth{t, o} {
		for h, f := range src.fields {
			dst, ok := out.fields[h]
			if !ok {
				dst = &truthField{tokens: map[string]*truthToken{}, docLengths: map[string]uint32{}}
				out.fields[h] = dst
			}
			dst.totalLen += f.totalLen
			dst.totalFreqs += f.totalFreqs
			for id, l := range f.docLengths {
				dst.docLengths[id] = l
			}
			for v, tt := range f.tokens {
				dt, ok := dst.tokens[v]
				if !ok {
					dt = &truthToken{freqs: map[string]uint32{}}
					dst.tokens[v] = dt
				}
				for id, fr := range tt.freqs {
					dt.freqs[id] = fr
				}
			}
		}
	}
	return out
}

// ---------- verification ----------

func verify(t *testing.T, label string, s *storage.Storage, tr *truth) {
	t.Helper()

	// 1. Documents table: exact ids in exact order
	if len(s.DocumentsIds) != len(tr.docs) {
		t.Fatalf("%s: doc count mismatch: got %d want %d", label, len(s.DocumentsIds), len(tr.docs))
	}
	docIdx := map[string]uint32{}
	for i := range s.DocumentsIds {
		got := string(s.DocumentsIds[i].Value.Bytes())
		if got != tr.docs[i] {
			t.Fatalf("%s: doc[%d] = %q want %q", label, i, got, tr.docs[i])
		}
		docIdx[got] = uint32(i)
	}

	// 2. Fields
	if len(s.Fields) != len(tr.fields) {
		t.Fatalf("%s: field count mismatch: got %d want %d", label, len(s.Fields), len(tr.fields))
	}
	for hash, tf := range tr.fields {
		field, ok := s.Fields[hash]
		if !ok {
			t.Fatalf("%s: field %d missing", label, hash)
		}

		// 2a. field-level stats
		if field.TotalDocumentsLength != tf.totalLen {
			t.Fatalf("%s: field %d TotalDocumentsLength got %d want %d", label, hash, field.TotalDocumentsLength, tf.totalLen)
		}
		if field.TotalTokenFrequenciesCount != tf.totalFreqs {
			t.Fatalf("%s: field %d TotalTokenFrequenciesCount got %d want %d", label, hash, field.TotalTokenFrequenciesCount, tf.totalFreqs)
		}
		var wantAvg float32
		if len(tf.docLengths) > 0 {
			wantAvg = float32(tf.totalLen) / float32(len(tf.docLengths))
		}
		if len(tf.docLengths) > 0 && math.Abs(float64(field.AvgDocumentLength-wantAvg)) > 1e-9 {
			t.Fatalf("%s: field %d avgdl got %v want %v", label, hash, field.AvgDocumentLength, wantAvg)
		}

		// 2b. document lengths: every (doc,len) pair present, none extra, sorted by index
		if len(field.DocumentLengths) != len(tf.docLengths) {
			t.Fatalf("%s: field %d doc-length count got %d want %d", label, hash, len(field.DocumentLengths), len(tf.docLengths))
		}
		var prev int64 = -1
		for _, dl := range field.DocumentLengths {
			if int64(dl.Index) <= prev {
				t.Fatalf("%s: field %d document lengths not strictly sorted at index %d", label, hash, dl.Index)
			}
			prev = int64(dl.Index)
			if int(dl.Index) >= len(tr.docs) {
				t.Fatalf("%s: field %d doc-length index %d out of range", label, hash, dl.Index)
			}
			id := tr.docs[dl.Index]
			want, ok := tf.docLengths[id]
			if !ok {
				t.Fatalf("%s: field %d unexpected doc-length for doc %q", label, hash, id)
			}
			if dl.Length != want {
				t.Fatalf("%s: field %d doc %q length got %d want %d", label, hash, id, dl.Length, want)
			}
		}

		// 2c. tokens: exact set, sorted, with exact per-document frequencies and posting bits
		if len(field.Tokens) != len(tf.tokens) {
			t.Fatalf("%s: field %d token count got %d want %d", label, hash, len(field.Tokens), len(tf.tokens))
		}
		var prevTok []byte
		var bm roaring.Bitmap
		for i := range field.Tokens {
			tok := &field.Tokens[i]
			val := string(tok.Value.Bytes())
			if prevTok != nil && string(prevTok) >= val {
				t.Fatalf("%s: field %d tokens not strictly sorted at %q", label, hash, val)
			}
			prevTok = append(prevTok[:0], val...)

			tt, ok := tf.tokens[val]
			if !ok {
				t.Fatalf("%s: field %d unexpected token %q", label, hash, val)
			}
			if tok.FrequencyCount != uint64(len(tt.freqs)) {
				t.Fatalf("%s: field %d token %q FrequencyCount got %d want %d", label, hash, val, tok.FrequencyCount, len(tt.freqs))
			}

			// frequencies slice
			if tok.FrequenciesIndex+tok.FrequencyCount > uint64(len(s.TokenFrequencies)) {
				t.Fatalf("%s: field %d token %q frequencies slice out of range", label, hash, val)
			}
			freqs := s.TokenFrequencies[tok.FrequenciesIndex : tok.FrequenciesIndex+tok.FrequencyCount]
			seen := map[uint32]bool{}
			var prevDoc int64 = -1
			for _, fe := range freqs {
				if int64(fe.DocumentIndex) <= prevDoc {
					t.Fatalf("%s: field %d token %q frequencies not sorted by doc index", label, hash, val)
				}
				prevDoc = int64(fe.DocumentIndex)
				if int(fe.DocumentIndex) >= len(tr.docs) {
					t.Fatalf("%s: field %d token %q freq doc index %d out of range", label, hash, val, fe.DocumentIndex)
				}
				id := tr.docs[fe.DocumentIndex]
				want, ok := tt.freqs[id]
				if !ok {
					t.Fatalf("%s: field %d token %q unexpected freq entry for doc %q", label, hash, val, id)
				}
				if fe.Frequency != want {
					t.Fatalf("%s: field %d token %q doc %q frequency got %d want %d", label, hash, val, id, fe.Frequency, want)
				}
				seen[fe.DocumentIndex] = true
			}
			if len(seen) != len(tt.freqs) {
				t.Fatalf("%s: field %d token %q missing frequency entries: got %d want %d", label, hash, val, len(seen), len(tt.freqs))
			}

			// posting list bitmap must equal exactly the set of docs
			if tok.PostingListIndex >= uint64(len(s.PostingLists)) {
				t.Fatalf("%s: field %d token %q posting list index out of range", label, hash, val)
			}
			s.PostingLists[tok.PostingListIndex].UnsafeBitmap(&bm)
			if bm.GetCardinality() != uint64(len(tt.freqs)) {
				t.Fatalf("%s: field %d token %q posting cardinality got %d want %d", label, hash, val, bm.GetCardinality(), len(tt.freqs))
			}
			for id := range tt.freqs {
				if !bm.Contains(docIdx[id]) {
					t.Fatalf("%s: field %d token %q posting list missing doc %q", label, hash, val, id)
				}
			}
		}
	}
}

// ---------- corpus generation ----------

func fh(name string) uint64 { return xxh3.HashString(name) }

func randToken(r *rand.Rand, vocab []string) string { return vocab[r.Intn(len(vocab))] }

func genDocs(r *rand.Rand, prefix string, n int, fieldNames []string, vocab []string) (docs []*storage.Document) {
	for i := 0; i < n; i++ {
		doc := &storage.Document{Id: storage.DocumentId{Value: storage.RawValueFrom(fmt.Sprintf("%s-%06d", prefix, i))}}
		nf := 1 + r.Intn(len(fieldNames))
		perm := r.Perm(len(fieldNames))[:nf]
		for _, fi := range perm {
			field := &storage.FieldDefinition{Hash: fh(fieldNames[fi])}
			ntok := r.Intn(12) // may be zero: field with no tokens
			seen := map[string]bool{}
			var length uint32
			for j := 0; j < ntok; j++ {
				v := randToken(r, vocab)
				if seen[v] {
					continue // caller contract: dedupe per doc
				}
				seen[v] = true
				f := uint32(1 + r.Intn(5))
				length += f
				field.Tokens = append(field.Tokens, &storage.TokenDefinition{Value: []byte(v), Frequency: f})
			}
			field.Length = length // zero-length fields exercised when ntok==0
			doc.Fields = append(doc.Fields, field)
		}
		docs = append(docs, doc)
	}
	return docs
}

func buildAndLoad(t *testing.T, dir, name string, docs []*storage.Document) *storage.Storage {
	t.Helper()
	var b storage.Storage
	b.BuildFrom(docs...)
	path := filepath.Join(dir, name)
	if err := b.SaveTo(path); err != nil {
		t.Fatalf("SaveTo(%s): %v", name, err)
	}
	var l storage.Storage
	if err := l.Load(path); err != nil {
		t.Fatalf("Load(%s): %v", name, err)
	}
	t.Cleanup(func() { l.Close() })
	return &l
}

// ---------- tests ----------

func TestAudit_BuildSaveLoad_RoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	dir := t.TempDir()
	fields := []string{"title", "body", "tags", "author"}
	vocab := make([]string, 300)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("tok%03d", i)
	}
	// include max-length (128B) and 1-byte tokens
	vocab = append(vocab, string(make([]byte, 0)))
	long := make([]byte, 128)
	for i := range long {
		long[i] = byte('a' + i%26)
	}
	vocab[0] = string(long)
	vocab[1] = "z"

	docs := genDocs(r, "rt", 500, fields, vocab)
	tr := newTruth()
	tr.addDocs(docs)

	s := buildAndLoad(t, dir, "roundtrip.idx", docs)
	verify(t, "roundtrip", s, tr)
}

func TestAudit_Merge_NoDataLoss(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	dir := t.TempDir()
	// A and B share some fields ("body","tags"), and each has exclusive ones
	fieldsA := []string{"body", "tags", "onlyA1", "onlyA2"}
	fieldsB := []string{"body", "tags", "onlyB1"}
	vocab := make([]string, 200)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("w%03d", i)
	}

	docsA := genDocs(r, "A", 400, fieldsA, vocab)
	docsB := genDocs(r, "B", 350, fieldsB, vocab)

	trA, trB := newTruth(), newTruth()
	trA.addDocs(docsA)
	trB.addDocs(docsB)

	a := buildAndLoad(t, dir, "a.idx", docsA)
	b := buildAndLoad(t, dir, "b.idx", docsB)
	verify(t, "A", a, trA)
	verify(t, "B", b, trB)

	m := storage.Merger{TempDir: dir}
	merged := filepath.Join(dir, "merged.idx")
	if err := m.Merge(merged, a, b); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	var s storage.Storage
	if err := s.Load(merged); err != nil {
		t.Fatalf("Load merged: %v", err)
	}
	defer s.Close()
	verify(t, "merged", &s, trA.merge(trB))
}

func TestAudit_Merge_Chained(t *testing.T) {
	r := rand.New(rand.NewSource(3))
	dir := t.TempDir()
	fields := []string{"f1", "f2", "f3"}
	vocab := make([]string, 80)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("c%02d", i)
	}

	var acc *storage.Storage
	trAcc := newTruth()
	m := storage.Merger{TempDir: dir}
	for round := range 4 {
		docs := genDocs(r, fmt.Sprintf("R%d", round), 120, fields, vocab)
		tr := newTruth()
		tr.addDocs(docs)
		seg := buildAndLoad(t, dir, fmt.Sprintf("seg%d.idx", round), docs)
		if acc == nil {
			acc, trAcc = seg, tr
			continue
		}
		out := filepath.Join(dir, fmt.Sprintf("acc%d.idx", round))
		if err := m.Merge(out, acc, seg); err != nil {
			t.Fatalf("Merge round %d: %v", round, err)
		}
		next := &storage.Storage{}
		if err := next.Load(out); err != nil {
			t.Fatalf("Load round %d: %v", round, err)
		}
		t.Cleanup(func() { next.Close() })
		acc = next
		trAcc = trAcc.merge(tr)
		verify(t, fmt.Sprintf("chain-round-%d", round), acc, trAcc)
	}
}

func TestAudit_Merge_EmptySides(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	dir := t.TempDir()
	fields := []string{"f"}
	vocab := []string{"x", "y", "zz"}

	empty := buildAndLoad(t, dir, "empty.idx", nil)
	docs := genDocs(r, "E", 50, fields, vocab)
	tr := newTruth()
	tr.addDocs(docs)
	full := buildAndLoad(t, dir, "full.idx", docs)

	m := storage.Merger{TempDir: dir}

	for _, tc := range []struct {
		name string
		a, b *storage.Storage
		tr   *truth
	}{
		{"empty+full", empty, full, tr},
		{"full+empty", full, empty, tr},
		{"empty+empty", empty, empty, newTruth()},
	} {
		out := filepath.Join(dir, tc.name+".idx")
		if err := m.Merge(out, tc.a, tc.b); err != nil {
			t.Fatalf("%s: Merge: %v", tc.name, err)
		}
		var s storage.Storage
		if err := s.Load(out); err != nil {
			t.Fatalf("%s: Load: %v", tc.name, err)
		}
		verify(t, tc.name, &s, tc.tr)
		s.Close()
	}
}

// Re-save a loaded (mmap-backed) storage and check it still round-trips:
// exercises SaveTo's Size invariant against a Storage whose Size came from Load.
func TestAudit_SaveTo_OfLoadedStorage(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	dir := t.TempDir()
	fields := []string{"a", "b"}
	vocab := make([]string, 60)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("v%02d", i)
	}
	docs := genDocs(r, "S", 200, fields, vocab)
	tr := newTruth()
	tr.addDocs(docs)

	loaded := buildAndLoad(t, dir, "orig.idx", docs)
	resaved := filepath.Join(dir, "resaved.idx")
	if err := loaded.SaveTo(resaved); err != nil {
		t.Fatalf("SaveTo of loaded storage: %v", err)
	}
	var s storage.Storage
	if err := s.Load(resaved); err != nil {
		t.Fatalf("Load resaved: %v", err)
	}
	defer s.Close()
	verify(t, "resaved", &s, tr)
}
