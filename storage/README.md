## Layout

```
┌─────────────────────────────────────────────────────┐
│                      HEADER                         │
│  magic (8B) | version (2B) | padding (6B)           │
│  total_docs (8B) | field_count (8B)                 │
│  total_posting_lists (8B)                           │
│  total_token_frequencies (8B)                       │
├─────────────────────────────────────────────────────┤
│                  DOC ID TABLE                       │
│  [doc_id_length (2B) | doc_id_bytes] × total_docs   │
│  (sorted alphabetically, position = internal ID)    │
├─────────────────────────────────────────────────────┤
│                 FIELD BLOCKS                        │
│  ┌───────────────────────────────────────────────┐  │
│  │  hash (8B) | avgdl (8B f64)                   │  │
│  │  token_count (8B) | doc_length_count (8B)     │  │
│  ├───────────────────────────────────────────────┤  │
│  │           DOC LENGTH ENTRIES                  │  │
│  │  [doc_index (8B) | length (8B)]               │  │
│  │  × doc_length_count                           │  │
│  │  (sorted by doc_index ascending)              │  │
│  ├───────────────────────────────────────────────┤  │
│  │             TOKEN ENTRIES                     │  │
│  │  [doc_freq_count (8B) |                       │  │
│  │   posting_list_index (8B) |                   │  │
│  │   frequencies_index (8B) |                    │  │
│  │   token_size (2B) | padding (6B) |            │  │
│  │   token_bytes] × token_count                  │  │
│  │  (sorted alphabetically)                      │  │
│  └───────────────────────────────────────────────┘  │
│  ...repeated for each field...                      │
├─────────────────────────────────────────────────────┤
│              POSTING LISTS REGION                   │
│  [bitmap_size (8B) | roaring bitmap bytes]          │
│  × total_posting_lists                              │
│  (indexed by posting_list_index)                    │
├─────────────────────────────────────────────────────┤
│           TOKEN FREQUENCIES REGION                  │
│  [doc_index (8B) | frequency (8B)]                  │
│  × total_token_frequencies                          │
│  (indexed by frequencies_index + doc_freq as count) │
└─────────────────────────────────────────────────────┘
```

## Invariants

- Doc IDs are sorted alphabetically. A document's position in the table is its internal sequential ID used in posting lists and TF entries.
- Doc length entries within each field block are sorted by doc_index ascending. This enables a merge scan during BM25 scoring instead of binary search.
- Token entries within each field block are sorted alphabetically by token bytes.
- TF entries for a given token occupy a contiguous slice: `TokenFrequencies[FrequenciesIndex : FrequenciesIndex+DocumentFrequencyCount]`. The writer must maintain this invariant.
- Posting lists and TF entries within a field are written in the same alphabetical token order, enabling sequential page access during sorted query processing.

## Storage.Size contract

`Storage.Size` is set by both `BuildFromSorted` and `LoadBytes`:

- After `BuildFromSorted`: exact byte count that `Save` will write. Use it to pre-allocate via `Truncate` + mmap before calling `Save`.
- After `LoadBytes`: bytes consumed from the source buffer. Supports multiple storages packed into one buffer — load the second at `src[s.Size:]`.

## Canonical write path

```go
var s Storage
s.BuildFromSorted(docs...)          // computes s.Size

f, _ := os.Create("index.bin")
f.Truncate(int64(s.Size))           // pre-allocate exact size

mapped, _ := mmap(f, s.Size, PROT_READ|PROT_WRITE)
s.Save(mapped[:0])                  // write into mmap'd file, no heap allocation
msync(mapped)
munmap(mapped)

os.Rename("index.bin.tmp", "index.bin")  // atomic swap
```

## Canonical read path

```go
f, _ := os.Open("index.bin")
mapped, _ := mmap(f, fileSize, PROT_READ)

var s Storage
s.LoadBytes(mapped)                 // zero-copy, points into mmap pages
                                    // btree built once at load time
// s is now queryable, all reads are O(log n) btree + O(1) slice access
```

## Posting list Unsafe flag

Posting lists loaded via `LoadBytes` have `Unsafe = true`, meaning their
internal bytes point into the mmap buffer. They must not be mutated in place.
Any modification must clone the bitmap first:

```go
if pl.Unsafe {
    cloned := pl.Bitmap.Clone()
    pl.Bitmap = *cloned
    pl.Unsafe = false
}
pl.Bitmap.Add(newDocID)
```

## Merge

Two storages can be merged assuming disjoint ordered doc ID ranges — every
doc ID in `b` sorts after every doc ID in `a`. This is guaranteed when the
corpus is partitioned by sorted doc ID range before building each batch.

```go
size := CalculateMergeSize(a, b)

f, _ := os.Create("merged.bin")
f.Truncate(int64(size))
mapped, _ := mmap(f, size, PROT_READ|PROT_WRITE)

Merge(mapped[:0], a, b)
msync(mapped)
munmap(mapped)

// load the merged index
var merged Storage
readOnly, _ := mmap(f, size, PROT_READ)
merged.LoadBytes(readOnly)
```

For testing and small corpora, `MergeStorages(a, b)` handles allocation
and loading in one call.

## BM25 scoring formula

```
score(doc, term) = idf(term) × (tf × (k1+1)) / (tf + k1 × (1 - b + b × docLen/avgdl))

idf(term) = log(1 + (totalDocs - docFreq + 0.5) / (docFreq + 0.5))
```

Where:
- `totalDocs` — from the header
- `docFreq` — `Token.DocumentFrequencyCount`
- `tf` — `TokenFrequencies[FrequenciesIndex + offset].Frequency` for the candidate doc
- `docLen` — `DocumentLengths[i].Length` for the candidate doc (binary search or merge scan)
- `avgdl` — `Field.AvgDocumentLength`
