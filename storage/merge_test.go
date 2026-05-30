package storage_test

import (
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

// ── CalculateMergeSize ────────────────────────────────────────────────────────

func TestCalculateMergeSize(t *testing.T) {
	type Test struct {
		name  string
		docsA []*storage.Document
		docsB []*storage.Document
	}

	tests := []Test{
		{
			name:  "two empty storages produce header-only size",
			docsA: nil,
			docsB: nil,
		},
		{
			name: "disjoint doc IDs disjoint tokens",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 3, testsuite.MakeToken("alpha", 3))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz", testsuite.MakeField(1, 2, testsuite.MakeToken("beta", 2))),
			},
		},
		{
			name: "disjoint doc IDs shared token",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 2, testsuite.MakeToken("shared", 2))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz", testsuite.MakeField(1, 3, testsuite.MakeToken("shared", 3))),
			},
		},
		{
			name: "field only in a",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 1, testsuite.MakeToken("foo", 1))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz", testsuite.MakeField(2, 1, testsuite.MakeToken("bar", 1))),
			},
		},
		{
			name: "multiple fields multiple tokens mix of shared and unique",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa",
					testsuite.MakeField(100, 5,
						testsuite.MakeToken("contrato", 3),
						testsuite.MakeToken("bogota", 2),
					),
					testsuite.MakeField(200, 2,
						testsuite.MakeToken("invias", 2),
					),
				),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz",
					testsuite.MakeField(100, 4,
						testsuite.MakeToken("contrato", 2),
						testsuite.MakeToken("medellin", 2),
					),
					testsuite.MakeField(300, 1,
						testsuite.MakeToken("nuevo", 1),
					),
				),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var a, b storage.Storage
			a.BuildFromSorted(tc.docsA...)
			b.BuildFromSorted(tc.docsB...)

			size := storage.CalculateMergeSize(&a, &b)

			// validate by actually merging and comparing byte lengths
			merged, err := storage.MergeStorages(&a, &b)
			if !assertions.NoError(err) {
				return
			}
			actual := merged.Size

			assertions.Equal(actual, size, "CalculateMergeSize must match actual merged byte count")
		})
	}
}

// ── MergeStorages doc ID table ────────────────────────────────────────────────

func TestMergeDocIds(t *testing.T) {
	type Test struct {
		name       string
		docsA      []*storage.Document
		docsB      []*storage.Document
		wantDocIDs []string
	}

	tests := []Test{
		{
			name:       "two empty storages produce empty merged doc table",
			docsA:      nil,
			docsB:      nil,
			wantDocIDs: []string{},
		},
		{
			name: "a docs come before b docs in merged table",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa"),
				testsuite.MakeDoc("bbb"),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("mmm"),
				testsuite.MakeDoc("zzz"),
			},
			wantDocIDs: []string{"aaa", "bbb", "mmm", "zzz"},
		},
		{
			name: "single doc in each storage",
			docsA: []*storage.Document{
				testsuite.MakeDoc("CO1.PCCNTR.001"),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("CO1.PCCNTR.002"),
			},
			wantDocIDs: []string{"CO1.PCCNTR.001", "CO1.PCCNTR.002"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var a, b storage.Storage
			a.BuildFromSorted(tc.docsA...)
			b.BuildFromSorted(tc.docsB...)

			merged, err := storage.MergeStorages(&a, &b)
			if !assertions.NoError(err) {
				return
			}

			if !assertions.Len(merged.DocumentsIds, len(tc.wantDocIDs)) {
				return
			}

			for i, wantID := range tc.wantDocIDs {
				t.Run(wantID, func(t *testing.T) {
					assertions := assert.New(t)
					assertions.Equal(storage.DocumentId(wantID), merged.DocumentsIds[i])
				})
			}
		})
	}
}

// ── MergeStorages field structure ─────────────────────────────────────────────

func TestMergeFieldStructure(t *testing.T) {
	type Test struct {
		name        string
		docsA       []*storage.Document
		docsB       []*storage.Document
		fieldHash   uint64
		wantAvgdl   float64
		wantDLCount int
	}

	tests := []Test{
		{
			name: "field only in a carries over unchanged",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 4, testsuite.MakeToken("foo", 4))),
			},
			docsB:       nil,
			fieldHash:   1,
			wantAvgdl:   4.0,
			wantDLCount: 1,
		},
		{
			name:  "field only in b carries over with shifted doc index",
			docsA: nil,
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz", testsuite.MakeField(1, 6, testsuite.MakeToken("bar", 6))),
			},
			fieldHash:   1,
			wantAvgdl:   6.0,
			wantDLCount: 1,
		},
		{
			name: "field in both storages merges doc lengths and recomputes avgdl",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 2, testsuite.MakeToken("foo", 2))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz", testsuite.MakeField(1, 8, testsuite.MakeToken("foo", 8))),
			},
			fieldHash:   1,
			wantAvgdl:   5.0, // (2+8)/2
			wantDLCount: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var a, b storage.Storage
			a.BuildFromSorted(tc.docsA...)
			b.BuildFromSorted(tc.docsB...)

			merged, err := storage.MergeStorages(&a, &b)
			if !assertions.NoError(err) {
				return
			}

			field, ok := merged.Fields[tc.fieldHash]
			if !assertions.True(ok, "field %x must exist in merged storage", tc.fieldHash) {
				return
			}

			assertions.InDelta(tc.wantAvgdl, field.AvgDocumentLength, 0.0001)
			assertions.Len(field.DocumentLengths, tc.wantDLCount)
		})
	}
}

// ── MergeStorages doc index shifting ─────────────────────────────────────────

func TestMergeDocIndexShifting(t *testing.T) {
	type Test struct {
		name          string
		docsA         []*storage.Document
		docsB         []*storage.Document
		fieldHash     uint64
		wantDLIndices []uint64
	}

	tests := []Test{
		{
			name: "b doc lengths shifted by len(a)",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 2, testsuite.MakeToken("foo", 2))),
				testsuite.MakeDoc("bbb", testsuite.MakeField(1, 3, testsuite.MakeToken("foo", 3))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("mmm", testsuite.MakeField(1, 4, testsuite.MakeToken("foo", 4))),
			},
			fieldHash:     1,
			wantDLIndices: []uint64{0, 1, 2}, // a: 0,1 — b: 2 (shifted by 2)
		},
		{
			name: "b TF entries use shifted document indices",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 1, testsuite.MakeToken("term", 1))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz", testsuite.MakeField(1, 5, testsuite.MakeToken("term", 5))),
			},
			fieldHash:     1,
			wantDLIndices: []uint64{0, 1},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var a, b storage.Storage
			a.BuildFromSorted(tc.docsA...)
			b.BuildFromSorted(tc.docsB...)

			merged, err := storage.MergeStorages(&a, &b)
			if !assertions.NoError(err) {
				return
			}

			field, ok := merged.Fields[tc.fieldHash]
			if !assertions.True(ok) {
				return
			}

			if !assertions.Len(field.DocumentLengths, len(tc.wantDLIndices)) {
				return
			}

			for i, wantIdx := range tc.wantDLIndices {
				assertions.Equal(wantIdx, field.DocumentLengths[i].Index,
					"doc length at position %d has wrong index", i)
			}
		})
	}
}

// ── MergeStorages posting lists ───────────────────────────────────────────────

func TestMergePostingLists(t *testing.T) {
	type Test struct {
		name           string
		docsA          []*storage.Document
		docsB          []*storage.Document
		fieldHash      uint64
		tokenValue     string
		wantDocIndices []uint64
		wantDocFreq    uint64
	}

	tests := []Test{
		{
			name: "token only in a has unchanged posting list",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 1, testsuite.MakeToken("onlya", 1))),
				testsuite.MakeDoc("bbb", testsuite.MakeField(1, 1, testsuite.MakeToken("onlya", 1))),
			},
			docsB:          nil,
			fieldHash:      1,
			tokenValue:     "onlya",
			wantDocIndices: []uint64{0, 1},
			wantDocFreq:    2,
		},
		{
			name: "token only in b has shifted posting list",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 1, testsuite.MakeToken("placeholder", 1))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("mmm", testsuite.MakeField(1, 1, testsuite.MakeToken("onlyb", 1))),
				testsuite.MakeDoc("zzz", testsuite.MakeField(1, 1, testsuite.MakeToken("onlyb", 1))),
			},
			fieldHash:      1,
			tokenValue:     "onlyb",
			wantDocIndices: []uint64{1, 2}, // shifted by len(a) = 1
			wantDocFreq:    2,
		},
		{
			name: "shared token merges posting lists with correct shifted indices",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 1, testsuite.MakeToken("shared", 1))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("mmm", testsuite.MakeField(1, 1, testsuite.MakeToken("shared", 1))),
				testsuite.MakeDoc("zzz", testsuite.MakeField(1, 1, testsuite.MakeToken("shared", 1))),
			},
			fieldHash:      1,
			tokenValue:     "shared",
			wantDocIndices: []uint64{0, 1, 2}, // a: 0 — b: 1,2 (shifted by 1)
			wantDocFreq:    3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var a, b storage.Storage
			a.BuildFromSorted(tc.docsA...)
			b.BuildFromSorted(tc.docsB...)

			merged, err := storage.MergeStorages(&a, &b)
			if !assertions.NoError(err) {
				return
			}

			field, ok := merged.Fields[tc.fieldHash]
			if !assertions.True(ok) {
				return
			}

			tok, ok := field.Tokens.Get(&storage.Token{Value: []byte(tc.tokenValue)})
			if !assertions.True(ok, "token %q must exist in merged field", tc.tokenValue) {
				return
			}

			assertions.Equal(tc.wantDocFreq, tok.DocumentFrequencyCount)

			pl := &merged.PostingLists[tok.PostingListIndex]
			var gotIndices []uint64
			it := pl.Iterator()
			for it.HasNext() {
				gotIndices = append(gotIndices, it.Next())
			}
			assertions.Equal(tc.wantDocIndices, gotIndices)
		})
	}
}

// ── MergeStorages token frequencies ──────────────────────────────────────────

func TestMergeTokenFrequencies(t *testing.T) {
	type Test struct {
		name       string
		docsA      []*storage.Document
		docsB      []*storage.Document
		fieldHash  uint64
		tokenValue string
		wantFreqs  []storage.TokenFrequencyEntry
	}

	tests := []Test{
		{
			name: "token only in a TF entries unchanged",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 3, testsuite.MakeToken("term", 3))),
			},
			docsB:      nil,
			fieldHash:  1,
			tokenValue: "term",
			wantFreqs:  []storage.TokenFrequencyEntry{{DocumentIndex: 0, Frequency: 3}},
		},
		{
			name: "token only in b TF entries use shifted doc index",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 1, testsuite.MakeToken("other", 1))),
				testsuite.MakeDoc("bbb", testsuite.MakeField(1, 1, testsuite.MakeToken("other", 1))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz", testsuite.MakeField(1, 7, testsuite.MakeToken("term", 7))),
			},
			fieldHash:  1,
			tokenValue: "term",
			wantFreqs:  []storage.TokenFrequencyEntry{{DocumentIndex: 2, Frequency: 7}}, // shifted by 2
		},
		{
			name: "shared token TF entries concatenated with shifted b entries",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa", testsuite.MakeField(1, 3, testsuite.MakeToken("shared", 3))),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("mmm", testsuite.MakeField(1, 5, testsuite.MakeToken("shared", 5))),
			},
			fieldHash:  1,
			tokenValue: "shared",
			wantFreqs: []storage.TokenFrequencyEntry{
				{DocumentIndex: 0, Frequency: 3},
				{DocumentIndex: 1, Frequency: 5}, // shifted by len(a) = 1
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var a, b storage.Storage
			a.BuildFromSorted(tc.docsA...)
			b.BuildFromSorted(tc.docsB...)

			merged, err := storage.MergeStorages(&a, &b)
			if !assertions.NoError(err) {
				return
			}

			field, ok := merged.Fields[tc.fieldHash]
			if !assertions.True(ok) {
				return
			}

			tok, ok := field.Tokens.Get(&storage.Token{Value: []byte(tc.tokenValue)})
			if !assertions.True(ok, "token %q must exist in merged field", tc.tokenValue) {
				return
			}

			freqs := merged.TokenFrequencies[tok.FrequenciesIndex : tok.FrequenciesIndex+tok.DocumentFrequencyCount]
			assertions.Equal(tc.wantFreqs, []storage.TokenFrequencyEntry(freqs))
		})
	}
}

// ── MergeStorages round-trip ──────────────────────────────────────────────────

func TestMergeRoundTrip(t *testing.T) {
	type Test struct {
		name  string
		docsA []*storage.Document
		docsB []*storage.Document
	}

	tests := []Test{
		{
			name:  "merge of two empty storages round-trips",
			docsA: nil,
			docsB: nil,
		},
		{
			name: "merge with no shared tokens round-trips",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa",
					testsuite.MakeField(1, 3,
						testsuite.MakeToken("alpha", 2),
						testsuite.MakeToken("beta", 1),
					),
				),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz",
					testsuite.MakeField(1, 2,
						testsuite.MakeToken("gamma", 2),
					),
				),
			},
		},
		{
			name: "merge with shared tokens round-trips",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa",
					testsuite.MakeField(1, 4,
						testsuite.MakeToken("contrato", 2),
						testsuite.MakeToken("bogota", 2),
					),
				),
				testsuite.MakeDoc("bbb",
					testsuite.MakeField(1, 2,
						testsuite.MakeToken("contrato", 2),
					),
				),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("mmm",
					testsuite.MakeField(1, 3,
						testsuite.MakeToken("contrato", 1),
						testsuite.MakeToken("medellin", 2),
					),
				),
				testsuite.MakeDoc("zzz",
					testsuite.MakeField(1, 1,
						testsuite.MakeToken("interventoria", 1),
					),
				),
			},
		},
		{
			name: "merge with field only in a and field only in b round-trips",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa",
					testsuite.MakeField(100, 2, testsuite.MakeToken("foo", 2)),
				),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz",
					testsuite.MakeField(200, 3, testsuite.MakeToken("bar", 3)),
				),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var a, b storage.Storage
			a.BuildFromSorted(tc.docsA...)
			b.BuildFromSorted(tc.docsB...)

			merged, err := storage.MergeStorages(&a, &b)
			if !assertions.NoError(err) {
				return
			}

			// round-trip the merged storage through Save + LoadBytes
			reloaded, err := testsuite.RoundTrip(merged)
			if !assertions.NoError(err) {
				return
			}

			// doc count matches
			if !assertions.Len(reloaded.DocumentsIds, len(tc.docsA)+len(tc.docsB)) {
				return
			}

			// field count matches
			expectedFields := make(map[uint64]struct{})
			for _, doc := range tc.docsA {
				for _, f := range doc.Fields {
					expectedFields[f.Hash] = struct{}{}
				}
			}
			for _, doc := range tc.docsB {
				for _, f := range doc.Fields {
					expectedFields[f.Hash] = struct{}{}
				}
			}
			assertions.Len(reloaded.Fields, len(expectedFields))

			// per-field token counts match merged
			for hash, mergedField := range merged.Fields {
				t.Run("field", func(t *testing.T) {
					assertions := assert.New(t)

					reloadedField, ok := reloaded.Fields[hash]
					if !assertions.True(ok, "field %x must survive merge round-trip", hash) {
						return
					}

					assertions.Equal(mergedField.Tokens.Len(), reloadedField.Tokens.Len())
					assertions.InDelta(mergedField.AvgDocumentLength, reloadedField.AvgDocumentLength, 0.0001)

					mergedField.Tokens.Scan(func(tok *storage.Token) bool {
						t.Run(string(tok.Value), func(t *testing.T) {
							assertions := assert.New(t)

							reloadedTok, ok := reloadedField.Tokens.Get(tok)
							if !assertions.True(ok, "token %q must survive merge round-trip", tok.Value) {
								return
							}

							assertions.Equal(tok.DocumentFrequencyCount, reloadedTok.DocumentFrequencyCount)

							mergedPL := &merged.PostingLists[tok.PostingListIndex]
							reloadedPL := &reloaded.PostingLists[reloadedTok.PostingListIndex]
							assertions.Equal(mergedPL.GetCardinality(), reloadedPL.GetCardinality())
						})
						return true
					})
				})
			}
		})
	}
}

// ── MergeStorages determinism ─────────────────────────────────────────────────

func TestMergeDeterminism(t *testing.T) {
	type Test struct {
		name  string
		docsA []*storage.Document
		docsB []*storage.Document
	}

	tests := []Test{
		{
			name: "same inputs produce identical merged bytes",
			docsA: []*storage.Document{
				testsuite.MakeDoc("aaa",
					testsuite.MakeField(1, 3,
						testsuite.MakeToken("alpha", 2),
						testsuite.MakeToken("beta", 1),
					),
				),
			},
			docsB: []*storage.Document{
				testsuite.MakeDoc("zzz",
					testsuite.MakeField(1, 2,
						testsuite.MakeToken("alpha", 1),
						testsuite.MakeToken("gamma", 1),
					),
				),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var a1, b1, a2, b2 storage.Storage
			a1.BuildFromSorted(tc.docsA...)
			b1.BuildFromSorted(tc.docsB...)
			a2.BuildFromSorted(tc.docsA...)
			b2.BuildFromSorted(tc.docsB...)

			size1 := storage.CalculateMergeSize(&a1, &b1)
			size2 := storage.CalculateMergeSize(&a2, &b2)
			assertions.Equal(size1, size2, "CalculateMergeSize must be deterministic")

			buf1 := storage.Merge(make([]byte, 0, size1), &a1, &b1)
			buf2 := storage.Merge(make([]byte, 0, size2), &a2, &b2)
			assertions.Equal(buf1, buf2, "Merge must be deterministic for identical inputs")
		})
	}
}
