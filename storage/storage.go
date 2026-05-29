package storage

import (
	"bytes"
	"fmt"
	"unsafe"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/tidwall/btree"
)

type Token struct {
	// Document frequency of the token in all documents
	DocumentFrequency uint64
	// Posting list of the documents for this token
	PostingListIndex uint64
	// Token frequencies per document
	FrequenciesIndex uint64
	// Actual content of the token
	Value []byte
}

func TokenLessFunc(a, b *Token) (less bool) {
	return bytes.Compare(a.Value, b.Value) == -1
}

type Field struct {
	// User for BM25 calculation
	AvgDocumentLength float64
	// Tokens present on the file
	// This field is stored in memory but most of its references
	// are direct mmap zero-copied arrays
	Tokens *btree.BTreeG[*Token]
	// DocumentLength entries
	// Keys are indexes of the documents
	DocumentLengths []DocumentLengthEntry
}

type DocumentId []byte

type Storage struct {
	// Reference of the internal buffer of the file
	// exposed only if the caller needs to hack his way around
	Buffer []byte
	// Fast reference to mapped fields for O(1) lookups
	Fields map[uint64]*Field
	// Documents mapped only unce to the sub-slices of buffer for quick convertion between
	// index form and human-readble form
	DocumentsIds []DocumentId
	// Posting lists used once the caller knows which fields-tokens to query
	PostingLists []roaring64.Bitmap
	// Token frequencies
	TokenFrequencies []TokenFrequencyEntry
}

func (s *Storage) LoadBytes(src []byte) (err error) {
	// Referencing the buffer permits the GC always have some part of the code pointing to it.
	// Meaning we can "safely" do unsafe references over it
	s.Buffer = src

	inUseBuffer := s.Buffer

	if uintptr(len(inUseBuffer)) < HeaderSize {
		return fmt.Errorf("passed buffer doesn't even have enough size for the file header")
	}

	// Zero copy access to the underlying buffer
	header := (*Header)(unsafe.Pointer(&inUseBuffer[0]))
	inUseBuffer = inUseBuffer[HeaderSize:]

	// TODO: In the future add magic number and version

	s.DocumentsIds = make([]DocumentId, 0, header.TotalDocuments)
	for index := range header.TotalDocuments {
		if uintptr(len(inUseBuffer)) < DocumentIdHeaderSize {
			return fmt.Errorf("not enough space for loading document: %d", index)
		}

		docIdHeader := (*DocumentIdHeader)(unsafe.Pointer(&inUseBuffer[0]))
		inUseBuffer = inUseBuffer[DocumentIdHeaderSize:]

		if len(inUseBuffer) < int(docIdHeader.Length) {
			return fmt.Errorf("not enough space for loading document id contents: %d: expecting at least: %d", index, docIdHeader.Length)
		}

		// Insert the reference into the table
		s.DocumentsIds = append(s.DocumentsIds, inUseBuffer[:docIdHeader.Length])
		inUseBuffer = inUseBuffer[docIdHeader.Length:]
	}

	s.Fields = make(map[uint64]*Field, header.FieldCount)
	var fieldsPrealloc = make([]Field, header.FieldCount)
	for range header.FieldCount {
		if uintptr(len(inUseBuffer)) < FieldHeaderSize {
			return fmt.Errorf("not enough space for loading fields from buffer")
		}

		fHeader := (*FieldHeader)(unsafe.Pointer(&inUseBuffer[0]))
		inUseBuffer = inUseBuffer[FieldHeaderSize:]

		field := &fieldsPrealloc[0]
		// Assign at once the field so we don't forget about it later
		s.Fields[fHeader.Hash] = field

		field.AvgDocumentLength = fHeader.AvgDocumentLength
		field.Tokens = btree.NewBTreeG(TokenLessFunc)
		fieldsPrealloc = fieldsPrealloc[1:]

		docsLengthSize := DocumentLengthEntrySize * uintptr(fHeader.DocumentLengthCount)
		if uintptr(len(inUseBuffer)) < docsLengthSize {
			return fmt.Errorf("not enough space for loading field's documents lengths from buffer")
		}

		field.DocumentLengths = unsafe.Slice(
			(*DocumentLengthEntry)(unsafe.Pointer(&inUseBuffer[0])),
			fHeader.DocumentLengthCount,
		)
		inUseBuffer = inUseBuffer[docsLengthSize:]

		var tokensPrealloc = make([]Token, fHeader.TokenCount)
		for range fHeader.TokenCount {
			if uintptr(len(inUseBuffer)) < TokenHeaderSize {
				return fmt.Errorf("not enough space for loading fields from buffer")
			}

			tHeader := (*TokenHeader)(unsafe.Pointer(&inUseBuffer[0]))
			inUseBuffer = inUseBuffer[TokenHeaderSize:]

			if len(inUseBuffer) < int(tHeader.Size) {
				return fmt.Errorf("not enough space for loading fields from buffer")
			}
			contents := inUseBuffer[:tHeader.Size]
			inUseBuffer = inUseBuffer[tHeader.Size:]

			token := &tokensPrealloc[0]
			token.DocumentFrequency = tHeader.DocumentFrequency
			token.PostingListIndex = tHeader.PostingListIndex
			token.FrequenciesIndex = tHeader.FrequenciesIndex
			token.Value = contents
			tokensPrealloc = tokensPrealloc[1:]

			field.Tokens.Set(token)
		}
	}

	s.PostingLists = make([]roaring64.Bitmap, header.TotalPostingLists)
	for index := range header.TotalPostingLists {
		if uintptr(len(inUseBuffer)) < PostingListHeaderSize {
			return fmt.Errorf("not enough space for loading fields from buffer")
		}

		pHeader := (*PostingListHeader)(unsafe.Pointer(&inUseBuffer[0]))
		inUseBuffer = inUseBuffer[PostingListHeaderSize:]

		if uint64(len(inUseBuffer)) < pHeader.Size {
			return fmt.Errorf("not enough space for loading posting list %d from buffer", index)
		}

		contents := inUseBuffer[:pHeader.Size]
		inUseBuffer = inUseBuffer[pHeader.Size:]

		// Zero copy loading of posting list
		s.PostingLists[index].FromUnsafeBytes(contents)
	}

	tokenFreqsSize := TokenFrequencyEntrySize * uintptr(header.TotalTokenFrequencies)
	if uintptr(len(inUseBuffer)) < tokenFreqsSize {
		return fmt.Errorf("not enough space for loading token frequencies from buffer")
	}

	s.TokenFrequencies = unsafe.Slice((*TokenFrequencyEntry)(unsafe.Pointer(&inUseBuffer[0])), header.TotalTokenFrequencies)
	return nil
}
