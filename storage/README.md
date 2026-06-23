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
│  [size (8B) | data (48B)] × total_docs              │
│  (fixed 56B stride; sorted alphabetically;          │
│   position = internal ID; mapped zero-copy)         │
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
│  │  [frequency_count (8B) |                      │  │
│  │   posting_list_index (8B) |                   │  │
│  │   frequencies_index (8B) |                    │  │
│  │   value_size (8B) | value_data (48B)]         │  │
│  │  × token_count                                │  │
│  │  (fixed 80B stride; sorted alphabetically)    │  │
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

- Doc IDs and token values are stored as `RawValue`: an 8-byte length plus a fixed `MaxRawValueSize`-byte (currently **128**) inline buffer. Values longer than the cap are truncated to it. The fixed stride is what allows the doc ID table and each field's token table to be mapped directly over the file as native Go slices (`unsafe.Slice`) with zero allocation and zero deserialization.
- Doc IDs are sorted alphabetically. A document's position in the table is its internal sequential ID used in posting lists and TF entries.
- Doc length entries within each field block are sorted by doc_index ascending. This enables a merge scan during BM25 scoring instead of binary search.
- Token entries within each field block are sorted alphabetically by token bytes, so they are binary-searched in place at query time — no btree is built at load. (A btree is used only as a transient accumulator during `BuildFrom`.)
- TF entries for a given token occupy a contiguous slice: `TokenFrequencies[FrequenciesIndex : FrequenciesIndex+FrequencyCount]`. The writer must maintain this invariant.
- Posting lists and TF entries within a field are written in the same alphabetical token order, enabling sequential page access during sorted query processing.

## Storage.Size contract

`Storage.Size` is set by both `BuildFrom` and `LoadBytes`:

- After `BuildFrom`: exact byte count that `SaveTo` will write. Use it to pre-allocate via `Truncate` + mmap before calling `Save`.
- After `LoadBytes`: bytes consumed from the source buffer. Supports multiple storages packed into one buffer — load the second at `src[s.Size:]`.

## Canonical write path

```go
var s Storage
s.BuildFrom(docs...)   // computes s.Size

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
                            // token tables mapped as fixed-stride slices
                            // (no btree rebuilt; binary search in place)
```

## Posting list decoding

A `PostingList` is a thin `{ Data []byte }` view into the mmap'd file. Decode
it into a roaring64 bitmap with `Bitmap`, which uses `FromUnsafeBytes` — the
bitmap aliases the mmap buffer with no copy:

```go
var bm roaring.Bitmap
pl.Bitmap(&bm)              // zero-copy decode into the caller-owned bitmap
```

Because the decoded bitmap points into read-only mmap pages, it must not be
mutated in place. Clone first if you need to modify it:

```go
owned := bm.Clone()
owned.Add(newDocID)
```

## Merge

`Merger.Merge` combines two storages into a single output file using a
streaming, temp-file-backed pipeline. No output size pre-computation is
required. The only precondition is that doc ID ranges are disjoint:
every doc ID in `b` must sort after every doc ID in `a`. This is guaranteed
when the corpus is partitioned by range before building each batch.

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
  concatenated (B's entries shifted), and `FrequencyCount` summed.
  `AvgDocumentLength` is recomputed from the merged `DocumentLengths` slice.

Field count in the output is `len(a.Fields) + len(b.Fields) - collisions`.

### Large-corpus ingestion pattern

For corpora too large to fit in one `BuildFrom` call, partition docs into
unsorted batches, build each batch into a segment file in parallel, then merge
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

With ~2 GB of heap per 1M-doc segment build and a merge that holds only the
working set (not the whole corpus), ingestion scales to arbitrarily large
corpora within a bounded memory envelope. This is not a projection — textiplex
indexed the **full 120 GB English Wikipedia export (25.65M documents)** into a
**70 GB index** on a single i9-10900K: ~35 min of segment creation (peak 27 GB
RAM) followed by ~113 min of merge (peak 12 GB RAM), ~2 h 28 min total. Bluge
and Bleve cannot complete this corpus — they OOM during segment combination.

## BM25 scoring formula

```
score(doc, term) = idf(term) × (tf × (k1+1)) / (tf + k1 × (1 - b + b × docLen/avgdl))

idf(term) = log(1 + (totalDocs - docFreq + 0.5) / (docFreq + 0.5))
```

Where:
- `totalDocs` — from the header
- `docFreq` — `Token.FrequencyCount`
- `tf` — `TokenFrequencies[FrequenciesIndex + offset].Frequency` for the candidate doc
- `docLen` — `DocumentLengths[i].Length` for the candidate doc (binary search or merge scan)
- `avgdl` — `Field.AvgDocumentLength`

## Benchmarks

All benchmarks run on an Intel Core i9-10900K @ 3.70GHz (10C/20T). The 1M-doc
corpus has 3 fields per document, 1 unique token per field (Bluge-equivalent).

| Operation | Time | Throughput | Heap | Allocs |
|---|---|---|---|---|
| BuildFrom (textiplex) | 2.84s | — | 2.12 GB | 33.5M |
| Merge 2×500K→1M (textiplex) | 1.35s | 375 MB/s | 0.71 GB | 19.5M |
| LoadBytes (textiplex) | 0.63s | 804 MB/s | 0.78 GB | 27.0M |
| OfflineWriter (Bluge fork) | 5.47s | — | 6.34 GB | 104.9M |
| OfflineWriter (Bluge upstream) | 12.28s | — | 8.20 GB | 131.0M |
| OfflineWriter (Bleve) | 24.28s | — | 10.07 GB | 146.5M |

Build vs Bluge fork: **1.9× faster, 3.0× less heap, 3.1× fewer allocs.**
Build vs Bluge upstream: **4.3× faster, 3.9× less heap, 3.9× fewer allocs.**
Build vs Bleve: **8.5× faster, 4.7× less heap, 4.4× fewer allocs.**

The fixed-stride layout makes `LoadBytes` a pure mmap-and-slice operation: doc
IDs, token tables, doc-length tables and token-frequency tables are mapped in
place with no per-record parsing, which is why load throughput reaches
804 MB/s and the merge runs at 375 MB/s while holding only the working set.

### Full English Wikipedia

The decisive result: textiplex indexed the **complete 120 GB English Wikipedia
export — 25,653,263 documents** (id + `title` + `content`, parsed with
`encoding/json/v2`) into a **70 GB index** on the same i9-10900K.

| Phase | Time | Peak RAM |
|---|---|---|
| Segment creation (indexing) | 35.1 min | 27 GB |
| Merge | 113.2 min | 12 GB |
| **Total** | **2 h 28 min** | — |

~205 GB/hour during segment creation, ~48.5 GB/hour end-to-end. **Bluge
(upstream and fork) and Bleve all OOM during the merge and never finish** —
textiplex is the only Go FTS engine able to fully index Wikipedia.
