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

- After `BuildFromSorted`: exact byte count that `SaveTo` will write. Use it to pre-allocate via `Truncate` + mmap before calling `Save`.
- After `LoadBytes`: bytes consumed from the source buffer. Supports multiple storages packed into one buffer — load the second at `src[s.Size:]`.

## Canonical write path

```go
var s Storage
s.BuildFromSorted(docs...)   // computes s.Size

err := s.SaveTo("index.bin") // Truncate + mmap + msync, atomic on success
```

Or manually with a pre-allocated mmap:

```go
f, _ := os.Create("index.bin")
f.Truncate(int64(s.Size))

mapped, _ := mmap(f, s.Size, PROT_READ|PROT_WRITE)
s.Save(mapped[:0])           // write into mmap'd region, no heap allocation
msync(mapped)
munmap(mapped)

os.Rename("index.bin.tmp", "index.bin") // atomic swap
```

## Canonical read path

```go
var s Storage
err := s.Load("index.bin")  // mmap + LoadBytes, zero-copy
defer s.Close()             // munmap + close fd

// s is now queryable, all reads are O(log n) btree + O(1) slice access
```

Or from a raw byte slice:

```go
f, _ := os.Open("index.bin")
mapped, _ := mmap(f, fileSize, PROT_READ)

var s Storage
s.LoadBytes(mapped)         // zero-copy, points into mmap pages
                            // btree built once at load time
```

## Posting list Unsafe flag

Posting lists loaded via `LoadBytes` or `Load` have `Unsafe = true`, meaning
their internal bytes point into the mmap buffer. They must not be mutated in
place. Any modification must clone the bitmap first:

```go
if pl.Unsafe {
    cloned := pl.Bitmap.Clone()
    pl.Bitmap = *cloned
    pl.Unsafe = false
}
pl.Bitmap.Add(newDocID)
```

## Merge

`Merger.Merge` combines two storages into a single output file using a
streaming, temp-file-backed pipeline. No output size pre-computation is
required. The only precondition is that doc ID ranges are disjoint and ordered:
every doc ID in `b` must sort after every doc ID in `a`. This is guaranteed
when the corpus is partitioned by sorted doc ID range before building each
batch.

```go
m := storage.Merger{TempDir: "/tmp"} // temp files written here during merge

err := m.Merge("merged.bin", &a, &b)

var merged Storage
err = merged.Load("merged.bin")
defer merged.Close()
```

### Merge semantics

Fields are classified into three groups per merge:

- **A-only fields**: written verbatim, indices resequenced via global cursors.
- **B-only fields**: written with all internal doc indices shifted by
  `len(a.DocumentsIds)`. Posting list bitmaps are re-encoded with the offset
  applied. Token frequency `DocumentIndex` values are shifted accordingly.
- **Collision fields** (same field hash in both): tokens are further
  classified as A-only, B-only, or shared. Shared tokens have their posting
  lists unioned (B's doc IDs shifted before OR), their frequency slices
  concatenated (B's entries shifted), and `DocumentFrequencyCount` summed.
  `AvgDocumentLength` is recomputed from the merged `DocumentLengths` slice.

Field count in the output is `len(a.Fields) + len(b.Fields) - collisions`.

### Large-corpus ingestion pattern

For corpora too large to fit in one `BuildFromSorted` call, partition docs into
sorted batches, build each batch into a segment file in parallel, then merge
pairwise:

```go
// Build segments in parallel (each worker owns a disjoint doc ID range)
segments := buildSegmentsParallel(batches) // []string of file paths

// Pairwise merge until one file remains
for len(segments) > 1 {
    next := mergePass(segments, m)
    segments = next
}
// segments[0] is the final index
```

With 32 GB RAM and ~2 GB heap per segment, up to 14 concurrent workers are
viable. At 3.4s per 1M-doc build and 2.2s per 1M-doc merge pass, a 30M-doc
index completes in roughly 20s end-to-end on modern hardware.

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

## Benchmarks

All benchmarks run on an Intel Core i9-10900K @ 3.70GHz, 1M documents, 3
fields per document, 1 unique token per field (Bluge-equivalent corpus).

| Operation | Time | Throughput | Heap | Allocs |
|---|---|---|---|---|
| BuildFromSorted (textiplex) | 3.4s | — | 2.0 GB | 33.7M |
| Merge 2×500K→1M (textiplex) | 2.2s | 161 MB/s | 732 MB | 16.7M |
| LoadBytes (textiplex) | 1.2s | 290 MB/s | 1.0 GB | 27.2M |
| OfflineWriter (Bluge fork) | 5.2s | — | 6.3 GB | 104.9M |
| OfflineWriter (Bluge upstream) | 15.7s | — | 10.9 GB | 216.5M |
| OfflineWriter (Bleve) | 24.0s | — | 10.1 GB | 146.5M |

Build vs Bluge fork: **1.5× faster, 3.2× less heap, 3.1× fewer allocs.**
Merge vs Bluge fork: **2.4× faster, 8.7× less heap, 6.3× fewer allocs.**
Merge vs Bluge upstream: **7.1× faster, 14.9× less heap, 13.0× fewer allocs.**
