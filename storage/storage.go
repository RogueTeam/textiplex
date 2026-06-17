package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"iter"
	"os"
	"slices"
	"unsafe"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/tidwall/btree"
	"golang.org/x/sys/unix"
)

type Tokens []Token

func (s *Tokens) GetString(ss string) (token *Token, found bool) {
	idx, found := slices.BinarySearchFunc(*s, ss, func(e Token, t string) int {
		return bytes.Compare(e.Value.Bytes(), unsafe.Slice(unsafe.StringData(t), len(t)))
	})

	if !found {
		return nil, false
	}
	return &(*s)[idx], true
}

func (s *Tokens) GetBytes(b []byte) (token *Token, found bool) {
	idx, found := slices.BinarySearchFunc(*s, b, func(e Token, t []byte) int {
		return bytes.Compare(e.Value.Bytes(), b)
	})

	if !found {
		return nil, false
	}
	return &(*s)[idx], true
}

func (s *Tokens) GetBytesOrNear(b []byte) (token *Token, found bool) {
	idx, found := slices.BinarySearchFunc(*s, b, func(e Token, t []byte) int {
		return bytes.Compare(e.Value.Bytes(), b)
	})

	if !found && idx >= len(*s) {
		return nil, false
	}
	return &(*s)[idx], true
}

func (s *Tokens) IterBytes(lo, hi []byte) (seq iter.Seq[*Token]) {
	if len(*s) == 0 {
		return
	}

	var startIndex, endIndex int
	if lo != nil {
		var found bool
		startIndex, found = slices.BinarySearchFunc(*s, lo, func(e Token, t []byte) int {
			return bytes.Compare(e.Value.Bytes(), t)
		})
		if !found && startIndex >= len(*s) {
			return
		}
	}

	if hi == nil {
		endIndex = len(*s) - 1
	} else {
		var found bool
		endIndex, found = slices.BinarySearchFunc(*s, hi, func(e Token, t []byte) int {
			return bytes.Compare(e.Value.Bytes(), t)
		})
		if !found && endIndex >= len(*s) {
			return
		}
	}

	return func(yield func(*Token) bool) {
		for i := startIndex; i <= endIndex; i++ {
			if !yield(&(*s)[i]) {
				return
			}
		}
	}
}

type Field struct {
	// Used for BM25 calculation
	AvgDocumentLength float64
	// Tokens present on the file
	// This field is stored in memory but most of its references
	// are direct mmap zero-copied arrays
	Tokens Tokens
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
	// File reference
	File *os.File
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

func (s *Storage) ColdInitialize() {
	s.Version = VersionV1
	s.Fields = make(map[uint64]*Field)
	s.Initialized = true
}

func (s *Storage) Reset() (err error) {
	err = s.Close()
	if err != nil {
		return fmt.Errorf("failed to first close the storage: %w", err)
	}
	*s = Storage{}
	return nil
}

// Builds the entire storage from a set of document definitions
func (s *Storage) BuildFrom(docs ...*Document) {
	if s.Initialized {
		s.Reset()
	}
	s.ColdInitialize()

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

	fieldAccPool := pool.New[FieldAccumulator](20)
	pdPool := pool.New[PostingData](20)
	bitmapPool := pool.New[roaring64.Bitmap](20)

	for docIndex, doc := range docs {
		s.DocumentsIds[docIndex] = doc.Id
		internalID := uint64(docIndex)

		// doc id header + doc id bytes
		s.Size += uint64(DocumentIdHeaderSize) + uint64(len(doc.Id))

		for _, fieldDef := range doc.Fields {
			fieldAccumulator, found := fieldsAccumulators[fieldDef.Hash]
			if !found {
				fieldAccumulator = fieldAccPool.Get()
				*fieldAccumulator = FieldAccumulator{
					Tokens: btree.NewBTreeGOptions(
						func(a, b *PostingData) bool {
							return bytes.Compare(a.Value, b.Value) == -1
						},
						btree.Options{NoLocks: true},
					),
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

			var queryPd PostingData
			for _, tokenDef := range fieldDef.Tokens {
				queryPd.Value = tokenDef.Value
				pd, found := fieldAccumulator.Tokens.Get(&queryPd)
				if !found {
					postingListsCounter++
					pd = pdPool.Get()
					*pd = PostingData{Value: tokenDef.Value, Bitmap: bitmapPool.Get()}
					fieldAccumulator.Tokens.Set(pd)
					s.Size += uint64(TokenSize)
				}

				pd.Bitmap.Add(internalID)
				pd.Freqs = append(pd.Freqs, TokenFrequencyEntry{
					DocumentIndex: internalID,
					Frequency:     tokenDef.Frequency,
				})
				tokensFreqsCounter++
				s.Size += uint64(TokenFrequencyEntrySize)
			}
		}
	}

	s.PostingLists = make([]PostingList, 0, postingListsCounter)
	s.TokenFrequencies = make([]TokenFrequencyEntry, 0, tokensFreqsCounter)

	var fieldsPool = pool.New[Field](20)
	for fieldHash, acc := range fieldsAccumulators {
		field := fieldsPool.Get()

		*field = Field{
			Tokens:          make([]Token, acc.Tokens.Len()),
			DocumentLengths: acc.DocumentsLengths,
		}
		if acc.DocumentsCount > 0 {
			field.AvgDocumentLength = float64(acc.TotalLength) / float64(acc.DocumentsCount)
		}

		it := acc.Tokens.Iter()

		var tokenIdx int
		for valid := it.First(); valid; valid = it.Next() {
			pd := it.Item()

			plIndex := uint64(len(s.PostingLists))
			s.PostingLists = append(s.PostingLists, PostingList{Bitmap: *pd.Bitmap})

			freqIndex := uint64(len(s.TokenFrequencies))
			s.TokenFrequencies = append(s.TokenFrequencies, pd.Freqs...)

			token := &field.Tokens[tokenIdx]
			tokenIdx++
			*token = Token{
				FrequencyCount:   pd.Bitmap.GetCardinality(),
				PostingListIndex: plIndex,
				FrequenciesIndex: freqIndex,
				Value:            TokenValueFrom(pd.Value),
			}
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

// This function will allocate a new batch and sort documents in the batch by their ID
// if the batch in ensured to be in order already call directly BuildFrom
func (s *Storage) SortAndBuildFrom(docs ...*Document) {
	docs = slices.Clone(docs)
	slices.SortFunc(docs, func(a, b *Document) int {
		return bytes.Compare(a.Id, b.Id)
	})

	s.BuildFrom(docs...)
}

// Saves the file to the target file
func (s *Storage) SaveTo(name string) (err error) {
	if !s.Initialized {
		// Cold initialize just to make sure we don't read an empty map
		s.ColdInitialize()
	}

	file, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("failed to create dst file: %w", err)
	}
	defer func() {
		file.Close()
		if err != nil {
			os.Remove(file.Name())
		}
	}()

	err = file.Truncate(int64(s.Size))
	if err != nil {
		return fmt.Errorf("failed to truncate file size: %w", err)
	}

	dst, err := unix.Mmap(
		int(file.Fd()),
		0,
		int(s.Size),
		unix.PROT_WRITE,
		unix.MAP_SHARED,
	)
	if err != nil {
		return fmt.Errorf("failed to mmap file: %w", err)
	}
	defer unix.Munmap(dst)

	out := dst[:0]

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

		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			newPlIndex := uint64(len(postingListCluster)) // Always update the index
			postingListCluster = append(postingListCluster, s.PostingLists[token.PostingListIndex])
			token.PostingListIndex = newPlIndex

			newFreqIndex := uint64(len(tokenFrequenciesCluster))
			tokenFrequenciesCluster = append(
				tokenFrequenciesCluster,
				s.TokenFrequencies[token.FrequenciesIndex:token.FrequenciesIndex+token.FrequencyCount]...,
			)
			token.FrequenciesIndex = newFreqIndex // Always update the index
		}
	}

	// Re-assign so the update of token indexes point to the new clusters
	s.PostingLists = postingListCluster
	s.TokenFrequencies = tokenFrequenciesCluster

	// Write fields
	for _, fieldHash := range fields {
		field := s.Fields[fieldHash]

		out = binary.NativeEndian.AppendUint64(out, fieldHash)
		out = binary.NativeEndian.AppendUint64(out, *(*uint64)(unsafe.Pointer(&field.AvgDocumentLength)))
		out = binary.NativeEndian.AppendUint64(out, uint64(len(field.Tokens)))
		out = binary.NativeEndian.AppendUint64(out, uint64(len(field.DocumentLengths)))

		for index := range field.DocumentLengths {
			docLength := &field.DocumentLengths[index]

			out = binary.NativeEndian.AppendUint64(out, docLength.Index)
			out = binary.NativeEndian.AppendUint64(out, docLength.Length)
		}

		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			out = binary.NativeEndian.AppendUint64(out, token.FrequencyCount)
			out = binary.NativeEndian.AppendUint64(out, token.PostingListIndex)
			out = binary.NativeEndian.AppendUint64(out, token.FrequenciesIndex)
			out = binary.NativeEndian.AppendUint64(out, token.Value.Size)
			out = append(out, token.Value.Data[:]...)
		}
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

	err = unix.Msync(dst, unix.MS_SYNC)
	if err != nil {
		return fmt.Errorf("failed to sync pages with FS: %w", err)
	}
	return nil
}

func (s *Storage) Close() (err error) {
	if s.File != nil {
		err = unix.Munmap(s.Buffer)
		if err != nil {
			return fmt.Errorf("failed to munmap buffer: %w", err)
		}
		s.Buffer = nil

		err = s.File.Close()
		if err != nil {
			return fmt.Errorf("failed to close file: %w", err)
		}
		s.File = nil
	}
	return nil
}

// Load the storage from a file source
func (s *Storage) Load(name string) (err error) {
	if s.Initialized {
		s.Initialized = false
	}

	s.File, err = os.Open(name)
	if err != nil {
		return fmt.Errorf("failed to open file handle: %w", err)
	}
	defer func() {
		if err != nil {
			s.File.Close()
			s.File = nil
		}
	}()

	info, err := s.File.Stat()
	if err != nil {
		return fmt.Errorf("failed to retrieve file information: %w", err)
	}

	size := info.Size()
	if uintptr(size) < HeaderSize {
		return fmt.Errorf("file doesn't even have enough space for header")
	}

	// Referencing the buffer permits the GC always have some part of the code pointing to it.
	// Meaning we can "safely" do unsafe references over it
	s.Buffer, err = unix.Mmap(
		int(s.File.Fd()),
		0,
		int(size),
		unix.PROT_READ,
		unix.MAP_PRIVATE,
	)
	if err != nil {
		return fmt.Errorf("failed mmap file: %w", err)
	}
	defer func() {
		if err != nil {
			unix.Munmap(s.Buffer)
			s.Buffer = nil
		}
	}()

	err = unix.Madvise(s.Buffer, unix.MADV_HUGEPAGE)
	if err != nil {
		return fmt.Errorf("failed to madvise mmapped buffer: %w", err)
	}

	inUseBuffer := s.Buffer

	if uintptr(len(inUseBuffer)) < HeaderSize {
		return fmt.Errorf("passed buffer doesn't even have enough space for the file header")
	}

	// Zero copy access to the underlying buffer
	header := (*Header)(unsafe.Pointer(&inUseBuffer[0]))
	inUseBuffer = inUseBuffer[HeaderSize:]

	// TODO: In the future add magic number and version
	s.Version = header.Version

	s.DocumentsIds = make([]DocumentId, header.TotalDocuments)
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
		s.DocumentsIds[index] = inUseBuffer[:docIdHeader.Length]
		inUseBuffer = inUseBuffer[docIdHeader.Length:]
	}

	s.Fields = make(map[uint64]*Field, header.FieldCount)
	var fieldsPool = pool.New[Field](20)
	for range header.FieldCount {
		if uintptr(len(inUseBuffer)) < FieldHeaderSize {
			return fmt.Errorf("not enough space for loading fields from buffer")
		}

		fHeader := (*FieldHeader)(unsafe.Pointer(&inUseBuffer[0]))
		inUseBuffer = inUseBuffer[FieldHeaderSize:]

		field := fieldsPool.Get()
		// Assign at once the field so we don't forget about it later
		s.Fields[fHeader.Hash] = field

		field.AvgDocumentLength = fHeader.AvgDocumentLength

		docsLengthSize := DocumentLengthEntrySize * uintptr(fHeader.DocumentLengthCount)
		if uintptr(len(inUseBuffer)) < docsLengthSize {
			return fmt.Errorf("not enough space for loading field's documents lengths from buffer")
		}

		field.DocumentLengths = unsafe.Slice(
			(*DocumentLengthEntry)(unsafe.Pointer(&inUseBuffer[0])),
			fHeader.DocumentLengthCount,
		)
		inUseBuffer = inUseBuffer[docsLengthSize:]

		tokensSubBufferSize := TokenSize * uintptr(fHeader.TokenCount)
		if uintptr(len(inUseBuffer)) < tokensSubBufferSize {
			return fmt.Errorf("not enough space for loading field's tokens from buffer")
		}
		field.Tokens = unsafe.Slice((*Token)(unsafe.Pointer(&inUseBuffer[0])), fHeader.TokenCount)
		inUseBuffer = inUseBuffer[tokensSubBufferSize:]
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

	s.Size = uint64(size) - uint64(len(inUseBuffer))

	s.Initialized = true

	return nil
}
