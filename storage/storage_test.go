package storage_test

import (
	"bytes"
	"os"
	"slices"
	"testing"

	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/testsuite"
	"github.com/stretchr/testify/assert"
)

// ── SortAndBuildFrom / BuildFrom ───────────────────────────────────────────────

func TestBuildFrom(t *testing.T) {
	type Test struct {
		name       string
		docs       []*storage.Document
		wantDocIDs []string
		wantErr    bool
	}

	tests := []Test{
		{
			name:       "empty input produces initialized empty storage",
			docs:       nil,
			wantDocIDs: []string{},
		},
		{
			name: "single document stored correctly",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 3, testsuite.MakeToken("foo", 2), testsuite.MakeToken("bar", 1))),
			},
			wantDocIDs: []string{"doc-a"},
		},
		{
			name: "documents sorted alphabetically by id",
			docs: []*storage.Document{
				testsuite.MakeDoc("zzz"),
				testsuite.MakeDoc("aaa"),
				testsuite.MakeDoc("mmm"),
			},
			wantDocIDs: []string{"aaa", "mmm", "zzz"},
		},
		{
			name: "multiple documents multiple fields",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-1",
					testsuite.MakeField(100, 5, testsuite.MakeToken("contrato", 3), testsuite.MakeToken("bogota", 2)),
					testsuite.MakeField(200, 2, testsuite.MakeToken("interventoria", 2)),
				),
				testsuite.MakeDoc("doc-2",
					testsuite.MakeField(100, 3, testsuite.MakeToken("contrato", 1), testsuite.MakeToken("medellin", 2)),
				),
			},
			wantDocIDs: []string{"doc-1", "doc-2"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var s storage.Storage
			s.SortAndBuildFrom(tc.docs...)

			if !assertions.True(s.Initialized) {
				return
			}
			if !assertions.Len(s.DocumentsIds, len(tc.wantDocIDs), "expecting different amount of docs") {
				return
			}

			for i, wantID := range tc.wantDocIDs {
				t.Run(wantID, func(t *testing.T) {
					assertions := assert.New(t)
					assertions.Equal(storage.DocumentId(wantID), s.DocumentsIds[i], "document id doesn't match")
				})
			}

			sorted := slices.IsSortedFunc(
				s.DocumentsIds,
				func(a, b storage.DocumentId) int {
					return bytes.Compare(a, b)
				},
			)
			assertions.True(sorted, "DocumentsIds must be sorted alphabetically")
		})
	}
}

// ── Field and token structure ─────────────────────────────────────────────────

func TestFieldStructure(t *testing.T) {
	type Test struct {
		name            string
		docs            []*storage.Document
		fieldHash       uint64
		wantTokenValues []string
		wantDocLengths  []storage.DocumentLengthEntry
		wantAvgdl       float64
	}

	tests := []Test{
		{
			name: "single field single doc token count and avgdl",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(42, 4,
					testsuite.MakeToken("alpha", 2),
					testsuite.MakeToken("beta", 2),
				)),
			},
			fieldHash:       42,
			wantTokenValues: []string{"alpha", "beta"},
			wantDocLengths:  []storage.DocumentLengthEntry{{Index: 0, Length: 4}},
			wantAvgdl:       4.0,
		},
		{
			name: "avgdl computed across multiple docs",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 2, testsuite.MakeToken("foo", 2))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(1, 8, testsuite.MakeToken("foo", 8))),
			},
			fieldHash:       1,
			wantTokenValues: []string{"foo"},
			wantDocLengths: []storage.DocumentLengthEntry{
				{Index: 0, Length: 2},
				{Index: 1, Length: 8},
			},
			wantAvgdl: 5.0,
		},
		{
			name: "doc with length zero not stored in doc lengths",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 0, testsuite.MakeToken("foo", 1))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(1, 3, testsuite.MakeToken("foo", 3))),
			},
			fieldHash: 1,
			wantDocLengths: []storage.DocumentLengthEntry{
				{Index: 1, Length: 3},
			},
			wantAvgdl: 3.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var s storage.Storage
			s.SortAndBuildFrom(tc.docs...)

			field, ok := s.Fields[tc.fieldHash]
			if !assertions.True(ok, "field %d must exist", tc.fieldHash) {
				return
			}

			assertions.InDelta(tc.wantAvgdl, field.AvgDocumentLength, 0.0001)
			assertions.Equal(tc.wantDocLengths, field.DocumentLengths)

			if tc.wantTokenValues != nil {
				for _, wantVal := range tc.wantTokenValues {
					t.Run(wantVal, func(t *testing.T) {
						assertions := assert.New(t)
						tok, ok := field.Tokens.Get(&storage.Token{Value: []byte(wantVal)})
						if !assertions.True(ok, "token %q must exist in field", wantVal) {
							return
						}
						assertions.Equal([]byte(wantVal), tok.Value)
					})
				}
			}
		})
	}
}

// ── Posting lists ─────────────────────────────────────────────────────────────

func TestPostingLists(t *testing.T) {
	type Test struct {
		name           string
		docs           []*storage.Document
		fieldHash      uint64
		tokenValue     string
		wantDocIndices []uint64
		wantDocFreq    uint64
	}

	tests := []Test{
		{
			name: "token in single doc has cardinality 1",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 3, testsuite.MakeToken("unique", 3))),
			},
			fieldHash:      1,
			tokenValue:     "unique",
			wantDocIndices: []uint64{0},
			wantDocFreq:    1,
		},
		{
			name: "token shared across docs has correct posting list",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 2, testsuite.MakeToken("shared", 1))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(1, 2, testsuite.MakeToken("shared", 2))),
				testsuite.MakeDoc("doc-c", testsuite.MakeField(1, 2, testsuite.MakeToken("other", 1))),
			},
			fieldHash:      1,
			tokenValue:     "shared",
			wantDocIndices: []uint64{0, 1},
			wantDocFreq:    2,
		},
		{
			name: "token in all docs",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-1", testsuite.MakeField(1, 1, testsuite.MakeToken("common", 1))),
				testsuite.MakeDoc("doc-2", testsuite.MakeField(1, 1, testsuite.MakeToken("common", 1))),
				testsuite.MakeDoc("doc-3", testsuite.MakeField(1, 1, testsuite.MakeToken("common", 1))),
			},
			fieldHash:      1,
			tokenValue:     "common",
			wantDocIndices: []uint64{0, 1, 2},
			wantDocFreq:    3,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var s storage.Storage
			s.SortAndBuildFrom(tc.docs...)

			field, ok := s.Fields[tc.fieldHash]
			if !assertions.True(ok) {
				return
			}

			tok, ok := field.Tokens.Get(&storage.Token{Value: []byte(tc.tokenValue)})
			if !assertions.True(ok, "token %q must exist", tc.tokenValue) {
				return
			}

			assertions.Equal(tc.wantDocFreq, tok.FrequencyCount)

			pl := &s.PostingLists[tok.PostingListIndex]
			var gotIndices []uint64
			it := pl.Iterator()
			for it.HasNext() {
				gotIndices = append(gotIndices, it.Next())
			}
			assertions.Equal(tc.wantDocIndices, gotIndices)
		})
	}
}

// ── Token frequencies ─────────────────────────────────────────────────────────

func TestTokenFrequencies(t *testing.T) {
	type Test struct {
		name       string
		docs       []*storage.Document
		fieldHash  uint64
		tokenValue string
		wantFreqs  []storage.TokenFrequencyEntry
	}

	tests := []Test{
		{
			name: "single doc single occurrence",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 1, testsuite.MakeToken("word", 1))),
			},
			fieldHash:  1,
			tokenValue: "word",
			wantFreqs:  []storage.TokenFrequencyEntry{{DocumentIndex: 0, Frequency: 1}},
		},
		{
			name: "single doc high frequency",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 7, testsuite.MakeToken("repeated", 7))),
			},
			fieldHash:  1,
			tokenValue: "repeated",
			wantFreqs:  []storage.TokenFrequencyEntry{{DocumentIndex: 0, Frequency: 7}},
		},
		{
			name: "token across multiple docs correct frequencies",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 3, testsuite.MakeToken("term", 3))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(1, 1, testsuite.MakeToken("term", 1))),
			},
			fieldHash:  1,
			tokenValue: "term",
			wantFreqs: []storage.TokenFrequencyEntry{
				{DocumentIndex: 0, Frequency: 3},
				{DocumentIndex: 1, Frequency: 1},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var s storage.Storage
			s.SortAndBuildFrom(tc.docs...)

			field, ok := s.Fields[tc.fieldHash]
			if !assertions.True(ok) {
				return
			}

			tok, ok := field.Tokens.Get(&storage.Token{Value: []byte(tc.tokenValue)})
			if !assertions.True(ok) {
				return
			}

			freqs := s.TokenFrequencies[tok.FrequenciesIndex : tok.FrequenciesIndex+tok.FrequencyCount]
			assertions.Equal(tc.wantFreqs, []storage.TokenFrequencyEntry(freqs))
		})
	}
}

// ── Round-trip Save / LoadBytes ───────────────────────────────────────────────

func TestRoundTrip(t *testing.T) {
	type Test struct {
		name string
		docs []*storage.Document
	}

	tests := []Test{
		{
			name: "empty storage round-trips cleanly",
			docs: nil,
		},
		{
			name: "single document single field single token",
			docs: []*storage.Document{
				testsuite.MakeDoc("CO1.PCCNTR.001",
					testsuite.MakeField(999, 5, testsuite.MakeToken("interventoria", 5)),
				),
			},
		},
		{
			name: "multiple documents multiple fields multiple tokens",
			docs: []*storage.Document{
				testsuite.MakeDoc("CO1.PCCNTR.001",
					testsuite.MakeField(100, 6,
						testsuite.MakeToken("contrato", 3),
						testsuite.MakeToken("bogota", 2),
						testsuite.MakeToken("interventoria", 1),
					),
					testsuite.MakeField(200, 2,
						testsuite.MakeToken("invias", 2),
					),
				),
				testsuite.MakeDoc("CO1.PCCNTR.002",
					testsuite.MakeField(100, 4,
						testsuite.MakeToken("contrato", 2),
						testsuite.MakeToken("medellin", 2),
					),
				),
				testsuite.MakeDoc("CO1.PCCNTR.003",
					testsuite.MakeField(100, 3,
						testsuite.MakeToken("interventoria", 3),
					),
					testsuite.MakeField(200, 1,
						testsuite.MakeToken("invias", 1),
					),
				),
			},
		},
		{
			name: "token with long value near 8-byte boundary",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 1, testsuite.MakeToken("abcdefgh", 1))),
				testsuite.MakeDoc("doc-b", testsuite.MakeField(1, 1, testsuite.MakeToken("abcdefghi", 1))),
			},
		},
		{
			name: "doc id at alignment boundary",
			docs: []*storage.Document{
				testsuite.MakeDoc("12345678"),
				testsuite.MakeDoc("123456789"),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var original storage.Storage
			original.SortAndBuildFrom(tc.docs...)

			loaded := testsuite.RoundTrip(t, &original)

			if !assertions.Len(loaded.DocumentsIds, len(original.DocumentsIds)) {
				return
			}
			for i, id := range original.DocumentsIds {
				assertions.Equal(id, loaded.DocumentsIds[i], "doc id at index %d", i)
			}

			assertions.Len(loaded.Fields, len(original.Fields))

			for hash, origField := range original.Fields {
				t.Run("field", func(t *testing.T) {
					assertions := assert.New(t)

					loadedField, ok := loaded.Fields[hash]
					if !assertions.True(ok, "field %x must survive round-trip", hash) {
						return
					}

					assertions.InDelta(origField.AvgDocumentLength, loadedField.AvgDocumentLength, 0.0001)
					assertions.Equal(origField.DocumentLengths, loadedField.DocumentLengths)
					assertions.Equal(origField.Tokens.Len(), loadedField.Tokens.Len())

					origField.Tokens.Scan(func(origTok *storage.Token) bool {
						t.Run(string(origTok.Value), func(t *testing.T) {
							assertions := assert.New(t)

							loadedTok, ok := loadedField.Tokens.Get(origTok)
							if !assertions.True(ok, "token %q must survive round-trip", origTok.Value) {
								return
							}

							assertions.Equal(origTok.FrequencyCount, loadedTok.FrequencyCount)
							assertions.Equal(origTok.Value, loadedTok.Value)

							origPL := &original.PostingLists[origTok.PostingListIndex]
							loadedPL := &loaded.PostingLists[loadedTok.PostingListIndex]
							assertions.Equal(origPL.GetCardinality(), loadedPL.GetCardinality())

							origFreqs := original.TokenFrequencies[origTok.FrequenciesIndex : origTok.FrequenciesIndex+origTok.FrequencyCount]
							loadedFreqs := loaded.TokenFrequencies[loadedTok.FrequenciesIndex : loadedTok.FrequenciesIndex+loadedTok.FrequencyCount]
							assertions.Equal([]storage.TokenFrequencyEntry(origFreqs), []storage.TokenFrequencyEntry(loadedFreqs))
						})
						return true
					})
				})
			}

			assertions.Len(loaded.PostingLists, len(original.PostingLists))
			assertions.Len(loaded.TokenFrequencies, len(original.TokenFrequencies))

			for i := range loaded.PostingLists {
				assertions.True(loaded.PostingLists[i].Unsafe, "loaded posting list %d must be Unsafe", i)
			}
		})
	}
}

// ── Reset and re-initialize ───────────────────────────────────────────────────

func TestReset(t *testing.T) {
	type Test struct {
		name string
		docs []*storage.Document
	}

	tests := []Test{
		{
			name: "reset clears all state and allows rebuild",
			docs: []*storage.Document{
				testsuite.MakeDoc("doc-a", testsuite.MakeField(1, 2, testsuite.MakeToken("foo", 2))),
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			var s storage.Storage
			s.SortAndBuildFrom(tc.docs...)
			if !assertions.True(s.Initialized) {
				return
			}
			if !assertions.NotEmpty(s.DocumentsIds) {
				return
			}

			s.Reset()
			assertions.False(s.Initialized)
			assertions.Nil(s.DocumentsIds)
			assertions.Nil(s.Fields)
			assertions.Nil(s.PostingLists)
			assertions.Nil(s.TokenFrequencies)

			s.SortAndBuildFrom(tc.docs...)
			assertions.True(s.Initialized)
			assertions.Len(s.DocumentsIds, len(tc.docs))
		})
	}
}

// ── LoadBytes error handling ──────────────────────────────────────────────────

func TestLoadBytesErrors(t *testing.T) {
	type Test struct {
		name    string
		buf     []byte
		wantErr string
	}

	tests := []Test{
		{
			name:    "empty buffer",
			buf:     []byte{},
			wantErr: "doesn't even have enough space",
		},
		{
			name:    "buffer smaller than header",
			buf:     make([]byte, 4),
			wantErr: "doesn't even have enough space",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertions := assert.New(t)

			filename := testsuite.TempFilename(t, "invalid_*.bin")
			err := os.WriteFile(filename, tc.buf, 0o700)
			if !assertions.NoError(err, "failed to write invalid buffer") {
				return
			}

			var s storage.Storage
			err = s.Load(filename)
			if !assertions.Error(err) {
				return
			}
			assertions.Contains(err.Error(), tc.wantErr)
		})
	}
}
