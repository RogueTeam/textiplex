## Layout

```
┌─────────────────────────────────────────────────────┐
│                      HEADER                         │
│  magic (8B) | version (2B) | padding (6B)           │
│  total_docs (4B) | padding (4B)                     │
│  field_count (8B)                                   │
│  total_posting_lists (8B)                           │
│  total_token_frequencies (8B)                       │
├─────────────────────────────────────────────────────┤
│                  DOC ID TABLE                       │
│  [size (8B) | data (128B)] × total_docs             │
│  (fixed 136B stride; sorted alphabetically;         │
│   position = internal ID; mapped zero-copy)         │
├─────────────────────────────────────────────────────┤
│           TOKEN FREQUENCIES REGION                  │
│  [doc_index (4B) | frequency (4B)]                  │
│  × total_token_frequencies                          │
│  (fixed 8B stride; indexed by frequencies_index)   │
├─────────────────────────────────────────────────────┤
│                 FIELD BLOCKS                        │
│  ┌───────────────────────────────────────────────┐  │
│  │  hash (8B) | avgdl (4B f32) | Padding (4B)    │  │
│  │  total_docs_length (8B)                       │  │
│  │  token_count (8B) | total_token_freqs (8B)    │  │
│  │  doc_length_count (8B)                        │  │
│  ├───────────────────────────────────────────────┤  │
│  │           DOC LENGTH ENTRIES                  │  │
│  │  [doc_index (4B) | length (4B)]               │  │
│  │  × doc_length_count (fixed 8B stride)         │  │
│  │  (sorted by doc_index ascending)              │  │
│  ├───────────────────────────────────────────────┤  │
│  │             TOKEN ENTRIES                     │  │
│  │  [frequency_count (8B) |                      │  │
│  │   posting_list_index (8B) |                   │  │
│  │   frequencies_index (8B) |                    │  │
│  │   value_size (8B) | value_data (128B)]        │  │
│  │  × token_count                                │  │
│  │  (fixed 160B stride; sorted alphabetically)   │  │
│  └───────────────────────────────────────────────┘  │
│  ...repeated for each field...                      │
├─────────────────────────────────────────────────────┤
│              POSTING LISTS REGION                   │
│  [bitmap_size (4B) | roaring bitmap bytes]          │
│  × total_posting_lists                              │
│  (indexed by posting_list_index)                    │
└─────────────────────────────────────────────────────┘
```

## Invariants

- Doc IDs and token values are stored as `RawValue`: an 8-byte length plus a fixed `MaxRawValueSize`-byte (currently **128**) inline buffer. The doc ID stride is therefore **136 B**; the token entry stride (3 × 8 B index fields + `RawValue`) is **160 B**; the doc-length entry stride is **8 B** (two `uint32` fields, no padding). The fixed stride is what allows the doc ID table and each field's token table to be mapped directly over the file as native Go slices (`unsafe.Slice`) with zero allocation and zero deserialization.
- Doc IDs are sorted alphabetically. A document's position in the table is its internal sequential ID used in posting lists and TF entries.
- Token frequencies are written immediately after the doc ID table, before the field blocks. Each `TokenFrequencyEntry` is a fixed 8 B record: `doc_index (uint32, 4B) | frequency (uint32, 4B)` — both fields are naturally aligned, so the struct carries no padding. The `FrequenciesIndex` in a token entry is an absolute offset into this region; the relevant slice is `TokenFrequencies[FrequenciesIndex : FrequenciesIndex+FrequencyCount]`.
- Doc length entries within each field block are sorted by doc_index ascending. This enables a merge scan during BM25 scoring instead of binary search.
- Token entries within each field block are sorted alphabetically by token bytes, so they are binary-searched in place at query time — no btree is built at load. (A btree is used only as a transient accumulator during `BuildFrom`.)
- Posting lists and TF entries within a field are written in the same alphabetical token order, enabling sequential page access during sorted query processing.

## Field widths and ceilings

Several count/size fields are stored as 32-bit integers to keep the on-disk records compact. These widths are deliberate and impose the following hard ceilings on a single storage file:

| Field | Type | Ceiling | Notes |
|---|---|---|---|
| `Header.TotalDocuments` / internal doc index | `uint32` | 4,294,967,295 docs | Internal doc IDs, TF `DocumentIndex`, and `DocumentLengthEntry.Index` are all `uint32`. |
| `TokenFrequencyEntry.Frequency` | `uint32` | 4.29B occurrences | Count of one token in one field of one document — never approached in practice. |
| `DocumentLengthEntry.Length` | `uint32` | 4.29B tokens | Per-document field length in tokens, used as `docLen` in BM25. |
| `PostingListHeader.Size` | `uint32` | 4 GiB per posting list | A roaring bitmap over a `uint32` doc-ID domain serializes to well under 1 GiB even fully dense, so this is not a practical limit. |

Because the doc-ID domain is itself `uint32`, no single posting list can serialize past the `uint32` size field, and no token frequency can exceed `uint32` — the width reductions are safe for any corpus that fits the 4.29B-document ceiling. Fields that index the *whole file* (`FieldCount`, `TotalPostingLists`, `TotalTokenFrequencies`, `FrequenciesIndex`, `PostingListIndex`, per-token `FrequencyCount`, and all byte offsets during load) remain 64-bit.

> **When shrinking these widths, keep buffer-length math 64-bit.** During load the remaining-buffer bounds check must compare `uint64(len(buffer))` against the (now 32-bit) record size — never `uint32(len(buffer))`. On a multi-GB index the buffer length exceeds `uint32` range, and truncating it there makes the posting-list bounds check compare against the low 32 bits of the length, which fails spuriously on exactly the large files this engine targets.

## Storage.Size contract

`Storage.Size` is set by both `BuildFrom` and `Load`:

- After `BuildFrom`: exact byte count that `SaveTo` will write. `SaveTo` uses it to `Truncate` + mmap the output before writing.
- After `Load`: number of bytes consumed from the mapped file (equal to the file size for a single-storage file).

## Canonical write path

```go
var s Storage
s.BuildFrom(docs...)          // computes s.Size

err := s.SaveTo("index.bin")  // Truncate(s.Size) + mmap(PROT_WRITE) + msync;
                              // writes directly into the mapped region with no
                              // heap staging buffer, removes the file on error
```

`SaveTo` owns the whole write: it truncates the output to `s.Size`, maps it
`PROT_WRITE`, appends every section into the mapped slice, and `msync`s. There is
no separate "write into a caller-provided buffer" entry point.

## Canonical read path

```go
var s Storage
err := s.Load("index.bin")   // open + mmap(PROT_READ, MAP_PRIVATE) + madvise
defer s.Close()              // munmap + close fd

// s is now queryable; token/doc/TF tables are mapped in place as fixed-stride
// slices (no btree rebuilt — tokens are binary-searched directly), and all
// reads are O(log n) search + O(1) slice access
```

`Load` maps the file read-only and points every table (`DocumentsIds`,
`TokenFrequencies`, each field's `DocumentLengths` and `Tokens`, and the posting
lists) directly into the mapped pages via `unsafe.Slice`. Nothing is copied out
of the mapping, so the `Storage` is valid only until `Close`.

## Posting list decoding

A `PostingList` is a thin `{ Data []byte }` view into the mmap'd file. Decode
it into a roaring bitmap with `UnsafeBitmap`, which clears the destination and
uses `FromUnsafeBytes` — the bitmap aliases the mmap buffer with no copy:

```go
var bm roaring.Bitmap
pl.UnsafeBitmap(&bm)        // clears bm, then zero-copy decode into it
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
corpora within a bounded memory envelope. This is not a projection: textiplex
indexed the **full 120 GB English Wikipedia export (25.65M documents)** into a
**70 GB index** on a single i9-10900K in **~33.6 min of segment creation** followed
by **~30.9 min of merge**, **~1 h 4.5 min total**. Bluge and Bleve cannot complete
this corpus; they OOM during segment combination.

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
| Load (textiplex) | 0.63s | 804 MB/s | 0.78 GB | 27.0M |
| OfflineWriter (Bluge fork) | 5.47s | — | 6.34 GB | 104.9M |
| OfflineWriter (Bluge upstream) | 12.28s | — | 8.20 GB | 131.0M |
| OfflineWriter (Bleve) | 24.28s | — | 10.07 GB | 146.5M |

Build vs Bluge fork: **1.9× faster, 3.0× less heap, 3.1× fewer allocs.**
Build vs Bluge upstream: **4.3× faster, 3.9× less heap, 3.9× fewer allocs.**
Build vs Bleve: **8.5× faster, 4.7× less heap, 4.4× fewer allocs.**

The fixed-stride layout makes `Load` a pure mmap-and-slice operation: doc
IDs, token tables, doc-length tables and token-frequency tables are mapped in
place with no per-record parsing, which is why load throughput reaches
804 MB/s and the merge runs at 375 MB/s while holding only the working set.

### Full English Wikipedia

The decisive result: textiplex indexed the **complete 120 GB English Wikipedia
export — 25,653,263 documents** (id + `title` + `content`, parsed with
`encoding/json/v2`) into a **70 GB index** on the same i9-10900K.

| Phase | Time | Peak RAM |
|---|---|---|
| Segment creation (indexing) | 33.6 min | 14 GB |
| Merge (parallel) | 30.9 min | see note below |
| **Total** | **1 h 4.5 min** | — |

> **Merge peak RAM is unmeasured in this run.** The 30.9 min figure comes from
> the parallel bottom-up merge (up to 8 workers). Because multiple pairwise
> merges run concurrently, peak resident memory is expected to exceed the
> 12 GB of the older serial merge and must be re-measured (e.g. `/usr/bin/time
> -v` maxrss) before quoting a number here. The `B/op` reported by `go test
> -benchmem` is cumulative allocation traffic, not resident set size, and is
> not a substitute.

~214 GB/hour during segment creation, ~112 GB/hour end-to-end. **Bluge
(upstream and fork) and Bleve all OOM during the merge and never finish** —
textiplex is the only Go FTS engine able to fully index Wikipedia.
