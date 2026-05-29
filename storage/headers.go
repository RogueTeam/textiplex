package storage

import "unsafe"

const MagicNumber uint64 = 0x7E7127E9

const (
	VersionV1 uint16 = iota
)

const HeaderSize = unsafe.Sizeof(Header{})

type Header struct {
	Magic   uint64
	Version uint16
	_/*Padding*/ [6]byte
	TotalDocuments        uint64
	FieldCount            uint64
	TotalPostingLists     uint64
	TotalTokenFrequencies uint64
}

const FieldHeaderSize = unsafe.Sizeof(FieldHeader{})

type FieldHeader struct {
	// xxh3 hashed representation of the field string
	Hash uint64
	// Avgdl used in the BM25 formula
	// Precomputed so the reader can go directly to queries
	AvgDocumentLength float64
	// Number of total tokens the field has
	TokenCount uint64
	// Number of document lengths included
	DocumentLengthCount uint64
}

const DocumentIdHeaderSize = unsafe.Sizeof(DocumentIdHeader{})

type DocumentIdHeader struct {
	Length uint16
}

const PostingListHeaderSize = unsafe.Sizeof(PostingListHeader{})

type PostingListHeader struct {
	Size uint64
}

const TokenFrequencyEntrySize = unsafe.Sizeof(TokenFrequencyEntry{})

type TokenFrequencyEntry struct {
	// The index of the document
	// Mapping this to a human readable key consist in
	// indexing the document id on the document id table
	DocumentIndex uint64
	// Token frequency on this document
	// this value is used by BM25
	Frequency uint64
}

const TokenHeaderSize = unsafe.Sizeof(TokenHeader{})

type TokenHeader struct {
	DocumentFrequencyCount uint64
	PostingListIndex       uint64
	FrequenciesIndex       uint64
	Size                   uint16
	_/*Padding */ [6]byte
}

const DocumentLengthEntrySize = unsafe.Sizeof(DocumentLengthEntry{})

// This is per field
// Meaning the length only references what the field is actually storing for that particular document
// Writer must ensure they are sorted based on index
type DocumentLengthEntry struct {
	// Index of the document referenced
	Index uint64
	// Actual length of the document in number of tokens
	Length uint64
}
