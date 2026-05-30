package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"unsafe"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/tidwall/btree"
)

type Token struct {
	// Document frequency of the token in all documents
	DocumentFrequencyCount uint64
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
	// Used for BM25 calculation
	AvgDocumentLength float64
	// Tokens present on the file
	// This field is stored in memory but most of its references
	// are direct mmap zero-copied arrays
	Tokens *btree.BTreeG[*Token]
	// DocumentLength entries
	// Keys are indexes of the documents
	DocumentLengths []DocumentLengthEntry
}

type PostingList struct {
	roaring64.Bitmap
	// When is true the underlying slice of the bitmap is considered read-only
	// new insertions rewrite calling bitmap.Clone and reassign
	Unsafe bool
}

type DocumentId []byte

type Storage struct {
	// Read-only intended field
	Version uint16
	// Read-only intended field
	Size uint64
	// Reference of the internal buffer of the file
	// exposed only if the caller needs to hack his way around
	Buffer []byte
	// Fast reference to mapped fields for O(1) lookups
	Fields map[uint64]*Field
	// Documents mapped only unce to the sub-slices of buffer for quick convertion between
	// index form and human-readble form
	DocumentsIds []DocumentId
	// Posting lists used once the caller knows which fields-tokens to query
	PostingLists []PostingList
	// Token frequencies
	TokenFrequencies []TokenFrequencyEntry
	// Used to determine if the storage was already initialized or not
	Initialized bool
}

func (s *Storage) coldInitialize() {
	s.Version = VersionV1
	s.Fields = make(map[uint64]*Field)
	s.Initialized = true
}

func (s *Storage) Reset() {
	*s = Storage{}
}

// Builds the entire storage from a set of document definitions
func (s *Storage) BuildFromSorted(docs ...*Document) {
	if s.Initialized {
		s.Reset()
	}
	s.coldInitialize()

	s.DocumentsIds = make([]DocumentId, len(docs))

	// Header is always fixed size
	s.Size = uint64(HeaderSize)

	type PostingData struct {
		Value  []byte
		Bitmap *roaring64.Bitmap
		Freqs  []TokenFrequencyEntry
	}
	type FieldAccumulator struct {
		TotalLength      uint64
		DocumentsCount   uint64
		DocumentsLengths []DocumentLengthEntry
		Tokens           *btree.BTreeG[*PostingData]
	}

	var postingListsCounter, tokensFreqsCounter uint64
	fieldsAccumulators := make(map[uint64]*FieldAccumulator)

	for docIndex, doc := range docs {
		s.DocumentsIds[docIndex] = doc.Id
		internalID := uint64(docIndex)

		// doc id header + doc id bytes
		s.Size += uint64(DocumentIdHeaderSize) + uint64(len(doc.Id))

		for _, fieldDef := range doc.Fields {
			fieldAccumulator, found := fieldsAccumulators[fieldDef.Hash]
			if !found {
				fieldAccumulator = &FieldAccumulator{
					Tokens: btree.NewBTreeG(func(a, b *PostingData) bool {
						return bytes.Compare(a.Value, b.Value) == -1
					}),
				}
				fieldsAccumulators[fieldDef.Hash] = fieldAccumulator

				// field header counted once per field
				s.Size += uint64(FieldHeaderSize)
			}

			if fieldDef.Length > 0 {
				fieldAccumulator.DocumentsLengths = append(fieldAccumulator.DocumentsLengths, DocumentLengthEntry{
					Index:  internalID,
					Length: fieldDef.Length,
				})
				fieldAccumulator.TotalLength += fieldDef.Length
				fieldAccumulator.DocumentsCount++

				// doc length entry
				s.Size += uint64(DocumentLengthEntrySize)
			}

			for _, tokenDef := range fieldDef.Tokens {
				pd, found := fieldAccumulator.Tokens.Get(&PostingData{Value: tokenDef.Value})
				if !found {
					postingListsCounter++
					pd = &PostingData{Value: tokenDef.Value, Bitmap: roaring64.New()}
					fieldAccumulator.Tokens.Set(pd)

					// token header + token bytes — new token only
					s.Size += uint64(TokenHeaderSize) + uint64(len(tokenDef.Value))
				}

				pd.Bitmap.Add(internalID)
				pd.Freqs = append(pd.Freqs, TokenFrequencyEntry{
					DocumentIndex: internalID,
					Frequency:     tokenDef.Frequency,
				})
				tokensFreqsCounter++

				// one TF entry per token per document always
				s.Size += uint64(TokenFrequencyEntrySize)
			}
		}
	}

	s.PostingLists = make([]PostingList, 0, postingListsCounter)
	s.TokenFrequencies = make([]TokenFrequencyEntry, 0, tokensFreqsCounter)

	var fieldsPrealloc = make([]Field, len(fieldsAccumulators))
	for fieldHash, acc := range fieldsAccumulators {
		field := &fieldsPrealloc[0]
		fieldsPrealloc = fieldsPrealloc[1:]
		*field = Field{
			Tokens:          btree.NewBTreeG(TokenLessFunc),
			DocumentLengths: acc.DocumentsLengths,
		}
		if acc.DocumentsCount > 0 {
			field.AvgDocumentLength = float64(acc.TotalLength) / float64(acc.DocumentsCount)
		}

		it := acc.Tokens.Iter()

		tokensPrealloc := make([]Token, acc.Tokens.Len())
		for valid := it.First(); valid; valid = it.Next() {
			pd := it.Item()

			plIndex := uint64(len(s.PostingLists))
			s.PostingLists = append(s.PostingLists, PostingList{Bitmap: *pd.Bitmap})

			freqIndex := uint64(len(s.TokenFrequencies))
			s.TokenFrequencies = append(s.TokenFrequencies, pd.Freqs...)

			token := &tokensPrealloc[0]
			tokensPrealloc = tokensPrealloc[1:]
			*token = Token{
				DocumentFrequencyCount: pd.Bitmap.GetCardinality(),
				PostingListIndex:       plIndex,
				FrequenciesIndex:       freqIndex,
				Value:                  pd.Value,
			}
			field.Tokens.Set(token)
		}
		it.Release()

		s.Fields[fieldHash] = field
	}

	clear(fieldsAccumulators)

	// posting list sizes only known after all docs are processed
	// roaring serialized size depends on final bitmap content
	for index := range s.PostingLists {
		s.Size += uint64(PostingListHeaderSize)
		s.Size += uint64(s.PostingLists[index].GetSerializedSizeInBytes())
	}
}

func (s *Storage) BuildFrom(docs ...*Document) {
	docs = slices.Clone(docs)
	slices.SortFunc(docs, func(a, b *Document) int {
		return bytes.Compare(a.Id, b.Id)
	})

	s.BuildFromSorted(docs...)
}

func (s *Storage) Save(dst []byte) (out []byte) {
	if !s.Initialized {
		// Cold initialize just to make sure we don't read an empty map
		s.coldInitialize()
	}

	out = dst

	// File Header
	out = binary.NativeEndian.AppendUint64(out, MagicNumber)
	out = binary.NativeEndian.AppendUint16(out, s.Version)
	out = append(out, 0, 0, 0, 0, 0, 0) // Padding 6 bytes
	out = binary.NativeEndian.AppendUint64(out, uint64(len(s.DocumentsIds)))
	out = binary.NativeEndian.AppendUint64(out, uint64(len(s.Fields)))
	out = binary.NativeEndian.AppendUint64(out, uint64(len(s.PostingLists)))
	out = binary.NativeEndian.AppendUint64(out, uint64(len(s.TokenFrequencies)))

	// Document Ids table
	for _, docId := range s.DocumentsIds {
		out = binary.NativeEndian.AppendUint16(out, uint16(len(docId)))
		out = append(out, docId...)
	}

	// Field pre-insertion
	// Clusters of posting lists related to the same Fields
	// They are keep near each other to reduce page-cache misses
	var postingListCluster = make([]PostingList, 0, len(s.PostingLists))
	// Token frequencies cluster lists related to the same Fields
	// They are keep near each other to reduce page-cache misses
	var tokenFrequenciesCluster = make([]TokenFrequencyEntry, 0, len(s.TokenFrequencies))
	// Field slices used for deterministic iteration since maps iterations could differ between runs
	var fields = make([]uint64, 0, len(s.Fields))
	for fieldHash := range s.Fields {
		fields = append(fields, fieldHash)
	}

	slices.Sort(fields)
	for _, fieldHash := range fields {
		field := s.Fields[fieldHash]

		it := field.Tokens.Iter()
		for valid := it.First(); valid; valid = it.Next() {
			token := it.Item()

			newPlIndex := uint64(len(postingListCluster)) // Always update the index
			postingListCluster = append(postingListCluster, s.PostingLists[token.PostingListIndex])
			token.PostingListIndex = newPlIndex

			newFreqIndex := uint64(len(tokenFrequenciesCluster))
			tokenFrequenciesCluster = append(
				tokenFrequenciesCluster,
				s.TokenFrequencies[token.FrequenciesIndex:token.FrequenciesIndex+token.DocumentFrequencyCount]...,
			)
			token.FrequenciesIndex = newFreqIndex // Always update the index
		}
		it.Release()
	}

	// Re-assign so the update of token indexes point to the new clusters
	s.PostingLists = postingListCluster
	s.TokenFrequencies = tokenFrequenciesCluster

	// Write fields
	for _, fieldHash := range fields {
		field := s.Fields[fieldHash]

		out = binary.NativeEndian.AppendUint64(out, fieldHash)
		out = binary.NativeEndian.AppendUint64(out, *(*uint64)(unsafe.Pointer(&field.AvgDocumentLength)))
		out = binary.NativeEndian.AppendUint64(out, uint64(field.Tokens.Len()))
		out = binary.NativeEndian.AppendUint64(out, uint64(len(field.DocumentLengths)))

		for index := range field.DocumentLengths {
			docLength := &field.DocumentLengths[index]

			out = binary.NativeEndian.AppendUint64(out, docLength.Index)
			out = binary.NativeEndian.AppendUint64(out, docLength.Length)
		}

		it := field.Tokens.Iter()
		for valid := it.First(); valid; valid = it.Next() {
			token := it.Item()

			out = binary.NativeEndian.AppendUint64(out, token.DocumentFrequencyCount)
			out = binary.NativeEndian.AppendUint64(out, token.PostingListIndex)
			out = binary.NativeEndian.AppendUint64(out, token.FrequenciesIndex)
			out = binary.NativeEndian.AppendUint16(out, uint16(len(token.Value)))
			out = append(out, 0, 0, 0, 0, 0, 0) // Padding 6 bytes
			out = append(out, token.Value...)

		}
		it.Release()
	}
	// Write posting lists
	var plBuffer bytes.Buffer
	for index := range postingListCluster {
		plBuffer.Reset()

		pl := &postingListCluster[index]
		size := pl.GetSerializedSizeInBytes()
		plBuffer.Grow(int(size))
		pl.WriteTo(&plBuffer)

		out = binary.NativeEndian.AppendUint64(out, size)
		out = append(out, plBuffer.Bytes()...)
	}
	// Write token frequencies
	for index := range tokenFrequenciesCluster {
		freq := &tokenFrequenciesCluster[index]

		out = binary.NativeEndian.AppendUint64(out, freq.DocumentIndex)
		out = binary.NativeEndian.AppendUint64(out, freq.Frequency)
	}
	return out
}

func (s *Storage) LoadBytes(src []byte) (err error) {
	if s.Initialized {
		s.Initialized = false
	}
	// Referencing the buffer permits the GC always have some part of the code pointing to it.
	// Meaning we can "safely" do unsafe references over it
	s.Buffer = src

	inUseBuffer := s.Buffer

	if uintptr(len(inUseBuffer)) < HeaderSize {
		return fmt.Errorf("passed buffer doesn't even have enough space for the file header")
	}

	// Zero copy access to the underlying buffer
	header := (*Header)(unsafe.Pointer(&inUseBuffer[0]))
	inUseBuffer = inUseBuffer[HeaderSize:]

	// TODO: In the future add magic number and version
	s.Version = header.Version

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
			token.DocumentFrequencyCount = tHeader.DocumentFrequencyCount
			token.PostingListIndex = tHeader.PostingListIndex
			token.FrequenciesIndex = tHeader.FrequenciesIndex
			token.Value = contents
			tokensPrealloc = tokensPrealloc[1:]

			field.Tokens.Set(token)
		}
	}

	s.PostingLists = make([]PostingList, header.TotalPostingLists)
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
		s.PostingLists[index].Unsafe = true
	}

	tokenFreqsSize := TokenFrequencyEntrySize * uintptr(header.TotalTokenFrequencies)
	if uintptr(len(inUseBuffer)) < tokenFreqsSize {
		return fmt.Errorf("not enough space for loading token frequencies from buffer")
	}

	if tokenFreqsSize > 0 {
		s.TokenFrequencies = unsafe.Slice((*TokenFrequencyEntry)(unsafe.Pointer(&inUseBuffer[0])), header.TotalTokenFrequencies)
		inUseBuffer = inUseBuffer[tokenFreqsSize:]
	}

	s.Size = uint64(len(src)) - uint64(len(inUseBuffer))

	s.Initialized = true
	return nil
}
