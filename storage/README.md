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
│  │  [doc_freq (8B) |                             │  │
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
