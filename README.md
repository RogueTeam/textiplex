# textiplex

A high-performance, low-memory full-text search engine written in Go. Built from first principles with zero OOP overhead, immutable mmap'd index files, and a streaming merge pipeline that outperforms every Go FTS engine benchmarked to date.

---

## Design philosophy

Most search engines in Go are ports or wrappers of JVM-era architectures — Lucene's segment model translated into Go, with all the object allocation patterns that implies. textiplex is built differently.

**Zero OOP.** No interface dispatch on the hot path. No virtual method tables. No heap allocation where a value or a slice will do. Functions operate on concrete types directly. The result is a build pipeline with 3× fewer allocations than the best available Go alternative.

**Immutable files.** An index segment is a single mmap'd file. Once written it is never modified in place. Queries read directly from OS-managed pages with zero deserialization cost. The page cache is your cache.

**Streaming merge.** Two segments are merged by streaming through temp files, rewriting doc ID offsets as bytes flow through. The merge pipeline never holds both input segments plus the output in memory simultaneously. A 1M-doc merge costs 732MB of heap — 8.7× less than the best available alternative.

**Ownership-aware bitmaps.** Posting lists loaded from mmap carry an `Unsafe` flag indicating their bytes are owned by the kernel page cache, not the heap. Code paths that would mutate them clone first. Code paths that only read skip the clone entirely.

---

## Benchmarks

All benchmarks run on an Intel Core i9-10900K @ 3.70GHz, 1M documents, 3 fields per document, 1 unique token per field. This corpus is the direct equivalent of Bluge's `BenchmarkOfflineWriter`.

### Index construction

| Engine | Time | Heap | Allocs |
|---|---|---|---|
| **textiplex** `BuildFromSorted` | **3.45s** | **2.04 GB** | **33.7M** |
| **textiplex** `Merge` 2×500K→1M | **2.20s** | **732 MB** | **16.7M** |
| **textiplex** `LoadBytes` | **1.21s** | **1.00 GB** | **27.2M** |
| Bluge fork | 5.20s | 6.34 GB | 104.9M |
| Bluge upstream | 15.75s | 10.95 GB | 216.5M |
| Bleve | 24.01s | 10.07 GB | 146.5M |

### Document construction pipeline (i7-1280P)

Measured on the fields package, which handles field hashing, token encoding, and pool allocation. These numbers represent the full cost from raw bytes to storage-ready documents.

| Operation | Time | Heap | Allocs |
|---|---|---|---|
| **textiplex** construction only (1M docs) | **320ms** | **373 MB** | **8.6M** |
| **textiplex** construction + build (1M docs) | **609ms** | **1.17 GB** | **11.6M** |
| testsuite helpers (naive, no pooling) | 627ms | 472 MB | 23.0M |

### Improvement ratios vs construction + build (609ms)

| Comparison | Time | Heap | Allocs |
|---|---|---|---|
| vs Bluge fork | 8.5× faster | 5.4× less | 9.0× fewer |
| vs Bluge upstream | 25.9× faster | 9.4× less | 18.7× fewer |
| vs Bleve | 39.4× faster | 8.6× less | 12.6× fewer |

### 30M document projection

On a 32GB machine with 1M-doc segments built in parallel:

| Engine | Real-world 30M indexing time |
|---|---|
| **textiplex** (fully parallel, 32GB) | **~30–45 seconds** |
| **textiplex** (same worker count as Bluge fork) | **~14 minutes** |
| Bluge fork (production setup) | ~2 hours |
| Bluge upstream | ~4–6 hours |

---

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
│  ├───────────────────────────────────────────────┤  │
│  │             TOKEN ENTRIES                     │  │
│  │  [doc_freq_count (8B) |                       │  │
│  │   posting_list_index (8B) |                   │  │
│  │   frequencies_index (8B) |                    │  │
│  │   token_size (2B) | padding (6B) |            │  │
│  │   token_bytes] × token_count                  │  │
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
- TF entries for a token are contiguous: `TokenFrequencies[FrequenciesIndex : FrequenciesIndex+DocumentFrequencyCount]`.

---

## Usage

### Write path

```go
var s storage.Storage
s.BuildFromSorted(docs...)   // computes exact output size

err := s.SaveTo("segment.bin") // Truncate + mmap + msync
```

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

All values are stored directly in the index — `docFreq` from `Token.DocumentFrequencyCount`, `tf` from `TokenFrequencies`, `docLen` from `DocumentLengths`, `avgdl` from `Field.AvgDocumentLength`, `totalDocs` from the header.

### Posting list Unsafe flag

```go
// Posting lists loaded from disk are mmap-backed and must not be mutated.
// Clone before modifying:
if pl.Unsafe {
    cloned := pl.Bitmap.Clone()
    pl.Bitmap = *cloned
    pl.Unsafe = false
}
pl.Bitmap.Add(newDocID)
```

---

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

---

## Status

textiplex is under active development. The storage layer and merge pipeline are complete and benchmarked. The query engine, tokenizers, and index writer are in progress. See the roadmap for the full picture.

The storage format is not yet stable. Breaking changes between versions are possible until a 1.0 release is tagged.

---

## Author

Built by [Antonio Donis](https://github.com/Shoriwe) / [ZED](mailto:antoniojosedonishung@gmail.com).
