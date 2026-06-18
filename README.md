# textiplex

A high-performance, low-memory full-text search engine written in Go. Built from first principles with zero OOP overhead, immutable mmap'd index files, and a streaming merge pipeline that outperforms every Go FTS engine benchmarked to date.

**textiplex is the only Go full-text search engine able to fully index English Wikipedia.** On a single desktop CPU it indexed the complete 120 GB export — 25.65M documents — into a 70 GB index inside a bounded memory envelope (27 GB peak during indexing, 12 GB during merge). Bluge (both upstream and a heavily optimized fork) and Bleve all ran out of memory and crashed during the merge, well before finishing. See [Benchmarks](#benchmarks).

## Design philosophy

Most search engines in Go are ports or wrappers of JVM-era architectures — Lucene's segment model translated into Go, with all the object allocation patterns that implies. textiplex is built differently.

**Zero OOP.** No interface dispatch on the hot path. No virtual method tables. No heap allocation where a value or a slice will do. Functions operate on concrete types directly. The result is a build pipeline with 3× fewer allocations than the best available Go alternative.

**Immutable files.** An index segment is a single mmap'd file. Once written it is never modified in place. Queries read directly from OS-managed pages with zero deserialization cost. The page cache is your cache.

**Streaming merge.** Two segments are merged by streaming through temp files, rewriting doc ID offsets as bytes flow through. The merge pipeline never holds both input segments plus the output in memory simultaneously, so peak memory tracks the working set, not the corpus size. This is what lets textiplex merge a 70 GB index inside 12 GB of RAM.

**Fixed-size records.** Doc IDs and token entries are fixed-stride records (a `RawValue` is an 8-byte length plus a 48-byte inline buffer). Because every record has the same size, the doc ID table and each field's token table are mapped directly over the mmap'd file as native Go slices with zero allocation and zero deserialization — no per-record length prefixes to walk, no btree to rebuild at load time. Sorted token tables are binary-searched in place. This single decision is what moved Wikipedia from "OOM at 5%" to "fully indexed".

**Ownership-aware bitmaps.** A posting list loaded from disk is just a `[]byte` slice pointing into the kernel page cache. Decoding it into a roaring bitmap (`PostingList.Bitmap`) is zero-copy via `FromUnsafeBytes`; any code path that needs to mutate clones first, while read-only paths skip the clone entirely.

## Benchmarks

All benchmarks run on an **Intel Core i9-10900K @ 3.70 GHz** (10 cores / 20 threads), Linux, amd64.

### Full English Wikipedia — the headline result

textiplex indexed the **complete English Wikipedia export end to end**. No other Go FTS engine tested could do this: Bluge upstream, the optimized Bluge fork, and Bleve all exhausted memory and crashed during the merge phase, never reaching completion.

The corpus is prepared as a single newline-delimited JSON file where each record carries a document id, a `title` field, and a `content` field. Parsing is done with `encoding/json/v2` (jsonv2), which is fast enough that decoding is effectively free — the measured time is almost entirely the indexing work itself, not I/O or parsing.

| | Value |
|---|---|
| Documents indexed | **25,653,263** |
| Source JSON | **120 GB** (jsonv2, streamed in ~1 GB batches) |
| Output index | **70 GB** (single immutable file) |
| Segment creation (indexing) | **35.1 min** — peak **27 GB** RAM |
| Merge | **113.2 min** — peak **12 GB** RAM |
| **Total wall time** | **2 h 28 min** |
| Indexing throughput | **~205 GB/hour** (segment creation) |
| End-to-end throughput | **~48.5 GB/hour** (including the full merge) |
| Sustained rate | ~12,200 docs/s indexing · ~2,900 docs/s end-to-end |

The entire run stayed inside a bounded memory envelope on a consumer desktop CPU — segment creation never exceeded 27 GB and the merge never exceeded 12 GB, with no swap. This is the property the fixed-size record layout and zero-copy mmap merge were built for: memory use is a function of the working set, not the corpus size.

> **Bluge and Bleve cannot index Wikipedia.** Both Bluge variants and Bleve OOM during segment combination far short of completion. Their memory use grows with the corpus, so the 120 GB dataset is simply out of reach on this hardware regardless of how long you are willing to wait.

### 1M-document construction

1M documents, 3 fields per document, 1 unique token per field — the direct equivalent of Bluge's `BenchmarkOfflineWriter`.

| Engine | Time | Throughput | Heap | Allocs |
|---|---|---|---|---|
| **textiplex** `BuildFrom` | **2.84s** | — | **2.12 GB** | **33.5M** |
| **textiplex** `Merge` 2×500K→1M | **1.35s** | **375 MB/s** | **0.71 GB** | **19.5M** |
| **textiplex** `LoadBytes` | **0.63s** | **804 MB/s** | **0.78 GB** | **27.0M** |
| Bluge fork (offline) | 5.47s | — | 6.34 GB | 104.9M |
| Bluge upstream (offline) | 12.28s | — | 8.20 GB | 131.0M |
| Bleve (offline) | 24.28s | — | 10.07 GB | 146.5M |

### Improvement ratios — `BuildFrom` (2.84s) vs full offline build

| Comparison | Time | Heap | Allocs |
|---|---|---|---|---|
| vs Bluge fork | 1.9× faster | 3.0× less | 3.1× fewer |
| vs Bluge upstream | 4.3× faster | 3.9× less | 3.9× fewer |
| vs Bleve | 8.5× faster | 4.7× less | 4.4× fewer |

### Query latency

Boolean query benchmarks, same 1M-doc corpus (lower is better):

| Query | textiplex | Bluge fork | Bluge upstream |
|---|---|---|---|---|
| Combined (Must + Should + MustNot) | **3.4 µs / 33 allocs** | 130 µs / 97 | 140 µs / 102 |
| Selective | **1.1 µs / 22 allocs** | 4.7 µs / 41 | 3.7 µs / 42 |
| Should | **308 µs / 34 allocs** | 353 µs / 2063 | 369 µs / 2065 |
| Must | **304 µs / 34 allocs** | 384 µs / 2074 | 419 µs / 2075 |

## File format

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
│   position = internal doc ID; loaded zero-copy)     │
├─────────────────────────────────────────────────────┤
│                 FIELD BLOCKS                        │
│  ┌───────────────────────────────────────────────┐  │
│  │  hash (8B) | avgdl (8B f64)                   │  │
│  │  token_count (8B) | doc_length_count (8B)     │  │
│  ├───────────────────────────────────────────────┤  │
│  │           DOC LENGTH ENTRIES                  │  │
│  │  [doc_index (8B) | length (8B)]               │  │
│  │  × doc_length_count                           │  │
│  ├───────────────────────────────────────────────┤  │
│  │             TOKEN ENTRIES                     │  │
│  │  [frequency_count (8B) |                      │  │
│  │   posting_list_index (8B) |                   │  │
│  │   frequencies_index (8B) |                    │  │
│  │   value_size (8B) | value_data (48B)]         │  │
│  │  × token_count                                │  │
│  │  (fixed 80B stride; sorted alphabetically;    │  │
│  │   binary-searchable in place, no btree)       │  │
│  └───────────────────────────────────────────────┘  │
├─────────────────────────────────────────────────────┤
│              POSTING LISTS REGION                   │
│  [bitmap_size (8B) | roaring64 bitmap bytes]        │
│  × total_posting_lists                              │
├─────────────────────────────────────────────────────┤
│           TOKEN FREQUENCIES REGION                  │
│  [doc_index (8B) | frequency (8B)]                  │
│  × total_token_frequencies                          │
└─────────────────────────────────────────────────────┘
```

### Invariants

- Doc IDs sorted alphabetically. Position in the table is the internal doc ID used in posting lists and TF entries.
- Doc length entries within each field sorted by `doc_index` ascending, enabling merge-scan during BM25 scoring.
- Token entries within each field sorted alphabetically, enabling binary search and range iteration.
- TF entries for a token are contiguous: `TokenFrequencies[FrequenciesIndex : FrequenciesIndex+FrequencyCount]`.

## Usage

### Write path

```go
var s storage.Storage
s.BuildFrom(docs...)         // computes exact output size (s.Size)

err := s.SaveTo("segment.bin") // Truncate + mmap + msync
```

`BuildFrom` expects doc IDs already sorted; `SortAndBuildFrom` sorts first if the batch is unordered.

### Read path

```go
var s storage.Storage
err := s.Load("segment.bin") // mmap, zero-copy, zero deserialization
defer s.Close()
```

### Merge two segments

```go
m := storage.Merger{TempDir: "/tmp"}
err := m.Merge("merged.bin", &a, &b)

var merged storage.Storage
err = merged.Load("merged.bin")
defer merged.Close()
```

Merge precondition: every doc ID in `b` must sort after every doc ID in `a`. Guaranteed when the corpus is partitioned by sorted doc ID range before building each segment.

### Large corpus ingestion

```go
// Build segments in parallel, each worker owns a disjoint sorted doc ID range
segments := buildSegmentsParallel(batches) // []string file paths

// Pairwise merge until one file remains
for len(segments) > 1 {
    segments = mergePass(segments, merger)
}
// segments[0] is the final index
```

### BM25 scoring

```
score(doc, term) =
    idf(term) × (tf × (k1+1)) / (tf + k1 × (1 - b + b × docLen/avgdl))

idf(term) =
    log(1 + (totalDocs - docFreq + 0.5) / (docFreq + 0.5))
```

All values are stored directly in the index — `docFreq` from `Token.FrequencyCount`, `tf` from `TokenFrequencies`, `docLen` from `DocumentLengths`, `avgdl` from `Field.AvgDocumentLength`, `totalDocs` from the header.

### Posting list decoding

```go
// A posting list loaded from disk is a []byte view into the mmap'd file.
// Decode it into a roaring64 bitmap (zero-copy) before querying:
var bm roaring64.Bitmap
pl.Bitmap(&bm)           // FromUnsafeBytes under the hood, no copy

// The decoded bitmap aliases mmap memory and must not be mutated in place.
// Clone first if you need to modify:
owned := bm.Clone()
owned.Add(newDocID)
```

## License

textiplex is licensed under the **GNU Affero General Public License v3.0 (AGPL-3.0)** with the **Commons Clause** addendum.

### What this means

**You can freely:**

- Use textiplex for personal projects, research, and non-commercial applications
- Study, modify, and distribute the source code under the same license
- Self-host textiplex for internal non-commercial use
- Contribute improvements — all contributions are welcome

**You cannot:**

- Sell textiplex or a product or hosted service whose value derives primarily from textiplex without a commercial license
- Use textiplex in a commercial product or internal commercial tooling without a commercial license

### Commons Clause

> The Software is provided to you by the Licensor under the License, as defined below, subject to the following condition.
>
> Without limiting other conditions in the License, the grant of rights under the License will not include, and the License does not grant to you, the right to Sell the Software.
>
> For purposes of the foregoing, "Sell" means practicing any or all of the rights granted to you under the License to provide to third parties, for a fee or other consideration, a product or service whose value derives, entirely or substantially, from the functionality of the Software.
>
> **Licensor:** Antonio Donis / ZED
> **License:** GNU Affero General Public License v3.0

### Commercial licensing

If you want to use textiplex in a commercial product, a closed internal tool, or a hosted service without open-sourcing your modifications, a commercial license is available. Contact **antoniojosedonishung@gmail.com** for pricing and terms.

### Contributions

By submitting a contribution you grant Antonio Donis / ZED a perpetual, worldwide, non-exclusive, royalty-free license to use, reproduce, modify, and sublicense your contribution under any terms, including commercial licenses. This allows textiplex to offer commercial licensing that includes community contributions without requiring individual contributor approval.

All contributions remain covered by the AGPL-3.0 + Commons Clause license for all other users.

## Status

textiplex is under active development. The storage layer, streaming merge pipeline, BM25 query engine, and tokenizers are complete and benchmarked — together they indexed the full English Wikipedia export end to end (see [Benchmarks](#benchmarks)). Ongoing work focuses on the public API surface and ergonomics.

The storage format is not yet stable. Breaking changes between versions are possible until a 1.0 release is tagged.

## Author

Built by [Antonio Donis](https://github.com/Shoriwe) / [ZED](mailto:antoniojosedonishung@gmail.com).
