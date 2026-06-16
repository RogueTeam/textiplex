package storage

import (
	"bytes"
	"unsafe"
)

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

const TokenSize = unsafe.Sizeof(Token{})

const MaxTokenSize = 48

type TokenValue struct {
	Size uint64
	Data [MaxTokenSize]byte
}

func CompareTokens(a, b Token) (cmp int) {
	return bytes.Compare(a.Value.Bytes(), b.Value.Bytes())
}

func TokenValueFrom[T ~string | ~[]byte](b T) (v TokenValue) {
	v.Size = uint64(copy(v.Data[:], b))
	return v
}

func (v *TokenValue) Bytes() (b []byte) {
	return v.Data[:min(MaxTokenSize, v.Size)]
}

func (v *TokenValue) UnsafeString() (s string) {
	return unsafe.String(&v.Data[0], min(MaxTokenSize, v.Size))
}

type Token struct {
	// Document frequency of the token in all documents
	FrequencyCount uint64
	// Posting list of the documents for this token
	PostingListIndex uint64
	// Token frequencies per document
	FrequenciesIndex uint64
	// Actual content of the token
	Value TokenValue
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
