package storage

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"iter"
	"os"
	"strings"
	"unsafe"

	"github.com/RoaringBitmap/roaring"
	"github.com/RogueTeam/textiplex/pointers"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/tidwall/btree"
	"golang.org/x/sys/unix"
)

type Tokens []Token

func (s *Tokens) BinarySearchString(ss string) (i int, found bool) {
	n := len((*s))
	// Define cmp(x[-1], target) < 0 and cmp(x[n], target) >= 0 .
	// Invariant: cmp(x[i - 1], target) < 0, cmp(x[j], target) >= 0.
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1) // avoid overflow when computing h
		// i ≤ h < j
		if strings.Compare((*s)[h].Value.UnsafeString(), ss) < 0 {
			i = h + 1 // preserves cmp(x[i - 1], target) < 0
		} else {
			j = h // preserves cmp(x[j], target) >= 0
		}
	}
	// i == j, cmp(x[i-1], target) < 0, and cmp(x[j], target) (= cmp(x[i], target)) >= 0  =>  answer is i.
	return i, i < n && (*s)[i].Value.UnsafeString() == ss
}

func (s *Tokens) BinarySearchBytes(b []byte) (i int, found bool) {
	n := len((*s))
	// Define cmp(x[-1], target) < 0 and cmp(x[n], target) >= 0 .
	// Invariant: cmp(x[i - 1], target) < 0, cmp(x[j], target) >= 0.
	i, j := 0, n
	for i < j {
		h := int(uint(i+j) >> 1) // avoid overflow when computing h
		// i ≤ h < j
		if bytes.Compare((*s)[h].Value.Bytes(), b) < 0 {
			i = h + 1 // preserves cmp(x[i - 1], target) < 0
		} else {
			j = h // preserves cmp(x[j], target) >= 0
		}
	}
	// i == j, cmp(x[i-1], target) < 0, and cmp(x[j], target) (= cmp(x[i], target)) >= 0  =>  answer is i.
	return i, i < n && bytes.Equal((*s)[i].Value.Bytes(), b)
}

func (s *Tokens) GetString(ss string) (token *Token, found bool) {
	idx, found := s.BinarySearchString(ss)

	if !found {
		return nil, false
	}
	return &(*s)[idx], true
}

func (s *Tokens) GetBytes(b []byte) (token *Token, found bool) {
	idx, found := s.BinarySearchBytes(b)
	if !found {
		return nil, false
	}
	return &(*s)[idx], true
}

func (s *Tokens) GetBytesOrNear(b []byte) (token *Token, found bool) {
	idx, found := s.BinarySearchBytes(b)

	if !found && idx >= len(*s) {
		return nil, false
	}
	return &(*s)[idx], true
}

func (s *Tokens) IterBytes(lo, hi []byte) (seq iter.Seq[*Token]) {
	if s == nil || len(*s) == 0 {
		return
	}

	var startIndex, endIndex int
	if lo != nil {
		var found bool
		startIndex, found = s.BinarySearchBytes(lo)
		if !found && startIndex >= len(*s) {
			return func(yield func(*Token) bool) {}
		}
	}

	if hi == nil {
		endIndex = len(*s) - 1
	} else {
		var found bool
		endIndex, found = s.BinarySearchBytes(hi)
		if !found && endIndex >= len(*s) {
			return func(yield func(*Token) bool) {}
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
	AvgDocumentLength float32
	// Computed on load time
	TotalDocumentsLength uint64
	// Tokens present on the file
	// This field is stored in memory but most of its references
	// are direct mmap zero-copied arrays
	Tokens Tokens
	// Sum of all token frequencies count
	TotalTokenFrequenciesCount uint64
	// DocumentLength entries
	// Keys are indexes of the documents
	DocumentLengths DocumentsLengths
}

type PostingList struct {
	Data []byte
}

// Clears the destination and loads the bitmap into it
func (l *PostingList) UnsafeBitmap(dst *roaring.Bitmap) {
	dst.Clear()
	if len(l.Data) > 0 {
		_, err := dst.FromUnsafeBytes(l.Data)
		if err != nil {
			dst.Clear()
		}
	}
}

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
	TokenFrequencies TokenFrequencies
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
		Value []byte
		Freqs []TokenFrequencyEntry
	}
	type FieldAccumulator struct {
		TotalLength                uint64
		DocumentsCount             uint64
		DocumentsLengths           []DocumentLengthEntry
		Tokens                     *btree.BTreeG[*PostingData]
		TotalTokenFrequenciesCount uint64
	}

	var postingListsCounter, tokensFreqsCounter uint64
	fieldsAccumulators := make(map[uint64]*FieldAccumulator)

	fieldAccPool := pool.New[FieldAccumulator](20)
	pdPool := pool.New[PostingData](20)

	for docIndex, doc := range docs {
		s.DocumentsIds[docIndex] = doc.Id
		docIdxUi32 := uint32(docIndex)

		// doc id header + doc id bytes
		s.Size += uint64(DocumentIdSize)

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
					Index:  docIdxUi32,
					Length: fieldDef.Length,
				})
				fieldAccumulator.TotalLength += uint64(fieldDef.Length)
				fieldAccumulator.DocumentsCount++

				// doc length entry
				s.Size += uint64(DocumentLengthEntrySize)
			}

			var queryPd PostingData
			for _, tokenDef := range fieldDef.Tokens {
				if tokenDef == nil {
					continue
				}
				queryPd.Value = tokenDef.Value
				pd, found := fieldAccumulator.Tokens.Get(&queryPd)
				if !found {
					postingListsCounter++
					pd = pdPool.Get()
					*pd = PostingData{Value: tokenDef.Value}
					fieldAccumulator.Tokens.Set(pd)
					s.Size += uint64(TokenSize)
				}

				pd.Freqs = append(pd.Freqs, TokenFrequencyEntry{
					DocumentIndex: docIdxUi32,
					Frequency:     tokenDef.Frequency,
				})
				tokensFreqsCounter++
				fieldAccumulator.TotalTokenFrequenciesCount++
				s.Size += uint64(TokenFrequencyEntrySize)
			}
		}
	}

	s.PostingLists = make([]PostingList, 0, postingListsCounter)
	s.TokenFrequencies = make([]TokenFrequencyEntry, 0, tokensFreqsCounter)

	// A single reused bitmap plus a reused scratch buffer. Posting lists are
	// serialized one token at a time from that token's frequency entries (which
	// are ascending by construction), so at most one roaring bitmap is live for
	// the entire pass instead of one per token.
	var workBitmap roaring.Bitmap
	var docIDs []uint32

	var fieldsPool = pool.New[Field](len(fieldsAccumulators))
	for fieldHash, acc := range fieldsAccumulators {
		field := fieldsPool.Get()

		*field = Field{
			Tokens:                     make([]Token, acc.Tokens.Len()),
			DocumentLengths:            acc.DocumentsLengths,
			TotalTokenFrequenciesCount: acc.TotalTokenFrequenciesCount,
			TotalDocumentsLength:       acc.TotalLength,
		}
		if acc.DocumentsCount > 0 {
			field.AvgDocumentLength = float32(acc.TotalLength) / float32(acc.DocumentsCount)
		}

		it := acc.Tokens.Iter()

		var tokenIdx int
		for valid := it.First(); valid; valid = it.Next() {
			pd := it.Item()

			// Rebuild the token's document set into the reused bitmap.
			workBitmap.Clear()
			if cap(docIDs) < len(pd.Freqs) {
				docIDs = make([]uint32, 0, len(pd.Freqs))
			}
			docIDs = docIDs[:0]
			for i := range pd.Freqs {
				docIDs = append(docIDs, pd.Freqs[i].DocumentIndex)
			}
			workBitmap.AddMany(docIDs)

			size := int(workBitmap.GetSerializedSizeInBytes())
			pdBytes, _ := workBitmap.MarshalBinary()

			s.Size += uint64(PostingListHeaderSize)
			s.Size += uint64(size)

			plIndex := uint64(len(s.PostingLists))
			s.PostingLists = append(s.PostingLists, PostingList{Data: pdBytes})

			freqIndex := uint64(len(s.TokenFrequencies))
			s.TokenFrequencies = append(s.TokenFrequencies, pd.Freqs...)

			token := &field.Tokens[tokenIdx]
			tokenIdx++
			*token = Token{
				FrequencyCount:   workBitmap.GetCardinality(),
				PostingListIndex: plIndex,
				FrequenciesIndex: freqIndex,
				Value:            RawValueFrom(pd.Value),
			}

			// The frequency slice is now copied into the contiguous region; drop
			// the per-token backing array so the GC can reclaim it during the
			// pass rather than all at once at the end.
			pd.Freqs = nil
		}
		it.Release()

		s.Fields[fieldHash] = field

		// Release this field's accumulator before moving on, bounding the live
		// working set to roughly a single field.
		acc.Tokens = nil
		delete(fieldsAccumulators, fieldHash)
	}
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

	var writeBuffer [8]byte
	dst := bufio.NewWriterSize(file, 4<<20)

	// File Header
	binary.NativeEndian.PutUint64(writeBuffer[:], MagicNumber)
	_, err = dst.Write(writeBuffer[:])
	if err != nil {
		return fmt.Errorf("failed to write magic number: %w", err)
	}
	binary.NativeEndian.PutUint16(writeBuffer[:], s.Version)
	_, err = dst.Write(writeBuffer[:])
	if err != nil {
		return fmt.Errorf("failed to write version: %w", err)
	}
	binary.NativeEndian.PutUint32(writeBuffer[:], uint32(len(s.DocumentsIds)))
	_, err = dst.Write(writeBuffer[:])
	if err != nil {
		return fmt.Errorf("failed to write number of documents: %w", err)
	}
	binary.NativeEndian.PutUint64(writeBuffer[:], uint64(len(s.Fields)))
	_, err = dst.Write(writeBuffer[:])
	if err != nil {
		return fmt.Errorf("failed to write number of fields: %w", err)
	}
	binary.NativeEndian.PutUint64(writeBuffer[:], uint64(len(s.PostingLists)))
	_, err = dst.Write(writeBuffer[:])
	if err != nil {
		return fmt.Errorf("failed to write number of posting lists: %w", err)
	}
	binary.NativeEndian.PutUint64(writeBuffer[:], uint64(len(s.TokenFrequencies)))
	_, err = dst.Write(writeBuffer[:])
	if err != nil {
		return fmt.Errorf("failed to write number of token frequencies: %w", err)
	}

	// Document Ids table
	if len(s.DocumentsIds) > 0 {
		_, err = dst.Write(pointers.UnsafeSliceBytes(s.DocumentsIds))
		if err != nil {
			return fmt.Errorf("failed to write document ids: %w", err)
		}
	}

	// Write token frequencies
	if len(s.TokenFrequencies) > 0 {
		_, err = dst.Write(pointers.UnsafeSliceBytes(s.TokenFrequencies))
		if err != nil {
			return fmt.Errorf("failed to write token frequencies bytes: %w", err)
		}
	}

	// Write fields
	for fieldHash, field := range s.Fields {
		_, err = dst.Write(pointers.UnsafeValueBytes(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write field hash: %w", err)
		}
		copy(writeBuffer[:], pointers.UnsafeValueBytes(&field.AvgDocumentLength))
		_, err = dst.Write(writeBuffer[:])
		if err != nil {
			return fmt.Errorf("failed to write field's avg doc length: %w", err)
		}
		_, err = dst.Write(pointers.UnsafeValueBytes(&field.TotalDocumentsLength))
		if err != nil {
			return fmt.Errorf("failed to write field's total document length: %w", err)
		}
		binary.NativeEndian.PutUint64(writeBuffer[:], uint64(len(field.Tokens)))
		_, err = dst.Write(writeBuffer[:])
		if err != nil {
			return fmt.Errorf("failed to write field's token count: %w", err)
		}
		_, err = dst.Write(pointers.UnsafeValueBytes(&field.TotalTokenFrequenciesCount))
		if err != nil {
			return fmt.Errorf("failed to write field's total token frequencies count: %w", err)
		}
		binary.NativeEndian.PutUint64(writeBuffer[:], uint64(len(field.DocumentLengths)))
		_, err = dst.Write(writeBuffer[:])
		if err != nil {
			return fmt.Errorf("failed to write field's document lengths count: %w", err)
		}

		if len(field.DocumentLengths) > 0 {
			_, err = dst.Write(pointers.UnsafeSliceBytes(field.DocumentLengths))
			if err != nil {
				return fmt.Errorf("failed to write field's documents lengths: %w", err)
			}
		}

		if len(field.Tokens) > 0 {
			_, err = dst.Write(pointers.UnsafeSliceBytes(field.Tokens))
			if err != nil {
				return fmt.Errorf("failed to write field's tokens: %w", err)
			}
		}
	}

	// Write posting lists
	for index := range s.PostingLists {
		pl := &s.PostingLists[index]

		length := uint32(len(pl.Data))
		_, err = dst.Write(pointers.UnsafeValueBytes(&length))
		if err != nil {
			return fmt.Errorf("failed to write posting list's length: %w", err)
		}
		_, err = dst.Write(pl.Data)
		if err != nil {
			return fmt.Errorf("failed to write posting list's contents: %w", err)
		}
	}

	err = dst.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush remaining contents: %w", err)
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
		unix.MAP_SHARED,
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

	err = unix.Madvise(s.Buffer, unix.MADV_SEQUENTIAL)
	if err != nil {
		return fmt.Errorf("failed to madvise sequential: %w", err)
	}
	err = unix.Madvise(s.Buffer, unix.MADV_HUGEPAGE)
	if err != nil {
		return fmt.Errorf("failed to madvise huge page: %w", err)
	}

	inUseBuffer := s.Buffer

	if uintptr(len(inUseBuffer)) < HeaderSize {
		return fmt.Errorf("passed buffer doesn't even have enough space for the file header")
	}

	// Zero copy access to the underlying buffer
	header := (*Header)(unsafe.Pointer(&inUseBuffer[0]))
	// TODO: In the future add magic number and version
	s.Version = header.Version

	inUseBuffer = inUseBuffer[HeaderSize:]
	if len(inUseBuffer) == 0 {
		return nil
	}

	docIdsSize := DocumentIdSize * uintptr(header.TotalDocuments)
	if uintptr(len(inUseBuffer)) < docIdsSize {
		return fmt.Errorf("not enough space for document ids")
	}

	if docIdsSize > 0 {
		s.DocumentsIds = unsafe.Slice((*DocumentId)(unsafe.Pointer(&inUseBuffer[0])), header.TotalDocuments)
		inUseBuffer = inUseBuffer[docIdsSize:]
	}

	tokenFreqsSize := TokenFrequencyEntrySize * uintptr(header.TotalTokenFrequencies)
	if uintptr(len(inUseBuffer)) < tokenFreqsSize {
		return fmt.Errorf("not enough space for loading token frequencies from buffer")
	}

	if tokenFreqsSize > 0 {
		s.TokenFrequencies = unsafe.Slice((*TokenFrequencyEntry)(unsafe.Pointer(&inUseBuffer[0])), header.TotalTokenFrequencies)
		inUseBuffer = inUseBuffer[tokenFreqsSize:]
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
		field.TotalDocumentsLength = fHeader.TotalDocumentsLength
		field.TotalTokenFrequenciesCount = fHeader.TotalTokenFrequencies

		docsLengthSize := DocumentLengthEntrySize * uintptr(fHeader.DocumentLengthCount)
		if uintptr(len(inUseBuffer)) < docsLengthSize {
			return fmt.Errorf("not enough space for loading field's documents lengths from buffer")
		}

		if docsLengthSize > 0 {
			field.DocumentLengths = unsafe.Slice(
				(*DocumentLengthEntry)(unsafe.Pointer(&inUseBuffer[0])),
				fHeader.DocumentLengthCount,
			)
			inUseBuffer = inUseBuffer[docsLengthSize:]
		}

		tokensSubBufferSize := TokenSize * uintptr(fHeader.TokenCount)
		if uintptr(len(inUseBuffer)) < tokensSubBufferSize {
			return fmt.Errorf("not enough space for loading field's tokens from buffer")
		}

		if tokensSubBufferSize > 0 {
			field.Tokens = unsafe.Slice((*Token)(unsafe.Pointer(&inUseBuffer[0])), fHeader.TokenCount)
			inUseBuffer = inUseBuffer[tokensSubBufferSize:]
		}
	}

	s.PostingLists = make([]PostingList, header.TotalPostingLists)
	for index := range header.TotalPostingLists {
		if uintptr(len(inUseBuffer)) < PostingListHeaderSize {
			return fmt.Errorf("not enough space for loading fields from buffer")
		}

		pHeader := (*PostingListHeader)(unsafe.Pointer(&inUseBuffer[0]))
		inUseBuffer = inUseBuffer[PostingListHeaderSize:]

		if uint64(len(inUseBuffer)) < uint64(pHeader.Size) {
			return fmt.Errorf("not enough space for loading posting list %d from buffer", index)
		}

		// Zero copy loading of posting list
		s.PostingLists[index].Data = inUseBuffer[:pHeader.Size]
		inUseBuffer = inUseBuffer[pHeader.Size:]
	}

	s.Size = uint64(size) - uint64(len(inUseBuffer))

	s.Initialized = true

	return nil
}
