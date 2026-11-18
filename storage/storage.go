package storage

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/RogueTeam/textiplex/pointers"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/tidwall/btree/v2"
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

type PostingLists struct {
	Reference []byte
	Headers   []PostingListHeader
	Entries   []PostingList
	// Number of documents in the storage. Posting lists read from a file are
	// rejected by Bitmap if they reference a document at or past it.
	DocumentCount uint64
}

func (p *PostingLists) WriteToFile(f *os.File, dst *bufio.Writer) (n int64, err error) {
	if len(p.Headers) > 0 {
		_, err := f.Seek(0, io.SeekEnd)
		if err != nil {
			return 0, fmt.Errorf("failed to seek to the end of the file: %w", err)
		}

		dst.Reset(f)

		nn, err := dst.Write(pointers.UnsafeSliceBytes(p.Headers))
		n += int64(nn)
		if err != nil {
			return n, fmt.Errorf("failed to write headers: %w", err)
		}

		nn, err = dst.Write(p.Reference)
		n += int64(nn)
		if err != nil {
			return n, fmt.Errorf("failed to write data: %w", err)
		}
	} else if len(p.Entries) > 0 {
		info, err := f.Stat()
		if err != nil {
			return 0, fmt.Errorf("failed to stat file: %w", err)
		}

		writeOffset := info.Size()
		plHeadersSize := PostingListHeaderSize * uintptr(p.Len())

		err = f.Truncate(writeOffset + int64(plHeadersSize))
		if err != nil {
			return 0, fmt.Errorf("failed to reserve headers space: %w", err)
		}

		_, err = f.Seek(0, io.SeekEnd)
		if err != nil {
			return 0, fmt.Errorf("failed to seek to the end of the file: %w", err)
		}

		dst.Reset(f)

		maxPlHeaders := MaxPostingListHeadersBatch(len(p.Entries))
		var headers = make([]PostingListHeader, 0, maxPlHeaders)
		var offset uint64
		for i, pl := range p.Entries {
			headers = append(headers, PostingListHeader{
				Offset: offset,
				Length: uint64(len(pl.Data)),
			})
			offset += uint64(len(pl.Data))
			if len(headers) >= maxPlHeaders {
				chunk := pointers.UnsafeSliceBytes(headers)
				nn, err := f.WriteAt(chunk, writeOffset)
				n += int64(nn)
				writeOffset += int64(nn)
				if err != nil {
					return n, fmt.Errorf("failed to write headers: %w", err)
				}
				headers = headers[:0]
			}

			nn, err := dst.Write(pl.Data)
			n += int64(nn)
			if err != nil {
				return n, fmt.Errorf("failed to write %d element data: %w", i, err)
			}
		}

		if len(headers) > 0 {
			nn, err := f.WriteAt(pointers.UnsafeSliceBytes(headers), writeOffset)
			n += int64(nn)
			writeOffset += int64(nn)
			if err != nil {
				return n, fmt.Errorf("failed to write remaining headers: %w", err)
			}
			headers = headers[:0]
		}
	}

	err = dst.Flush()
	if err != nil {
		return n, fmt.Errorf("failed to flush pending bytes: %w", err)
	}
	err = f.Sync()
	if err != nil {
		return n, fmt.Errorf("failed to sync file: %w", err)
	}
	return n, nil
}

func (p *PostingLists) Len() (n int) {
	return max(len(p.Headers), len(p.Entries))
}

func (p *PostingLists) DataSize() (n int) {
	if len(p.Headers) > 0 {
		for _, header := range p.Headers {
			n += int(header.Length)
		}
		return n
	}
	for _, entry := range p.Entries {
		n += len(entry.Data)
	}
	return n
}

// Get returns the posting list at idx. An index or a header that points
// outside the storage yields an empty posting list instead of panicking, so a
// damaged file degrades query results rather than crashing the process.
// Prefer Bitmap, which also rejects posting lists that reference documents
// the storage does not have, and does not allocate.
func (p *PostingLists) Get(idx int64) (pl *PostingList) {
	pl, err := p.GetChecked(idx)
	if err != nil {
		return &PostingList{}
	}
	return pl
}

// GetChecked returns the posting list at idx, or an error wrapping ErrCorrupt
// if idx or its header points outside the storage.
func (p *PostingLists) GetChecked(idx int64) (pl *PostingList, err error) {
	data, err := p.data(idx)
	if err != nil {
		return nil, err
	}
	if len(p.Headers) == 0 {
		return &p.Entries[idx], nil
	}
	return &PostingList{Data: data}, nil
}

// data returns the serialized posting list at idx without allocating.
func (p *PostingLists) data(idx int64) (data []byte, err error) {
	switch {
	case len(p.Headers) > 0:
		if idx < 0 || idx >= int64(len(p.Headers)) {
			return nil, corruptf("posting list index %d out of range [0, %d)", idx, len(p.Headers))
		}
		header := &p.Headers[idx]
		end := header.Offset + header.Length
		if end < header.Offset || end > uint64(len(p.Reference)) {
			return nil, corruptf("posting list %d spans [%d, %d+%d) outside %d bytes of posting list data",
				idx, header.Offset, header.Offset, header.Length, len(p.Reference))
		}
		// Cap the capacity so the bitmap cannot reach the next posting list.
		return p.Reference[header.Offset:end:end], nil
	case len(p.Entries) > 0:
		if idx < 0 || idx >= int64(len(p.Entries)) {
			return nil, corruptf("posting list index %d out of range [0, %d)", idx, len(p.Entries))
		}
		return p.Entries[idx].Data, nil
	default:
		return nil, corruptf("posting list index %d out of range: storage has no posting lists", idx)
	}
}

// Bitmap clears dst and decodes posting list idx into it without copying (see
// PostingList.UnsafeBitmap for the aliasing rules). A missing or undecodable
// posting list, or one read from a file that references a document past
// DocumentCount, leaves dst empty and returns an error wrapping ErrCorrupt.
func (p *PostingLists) Bitmap(idx int64, dst *roaring.Bitmap) (err error) {
	data, err := p.data(idx)
	if err != nil {
		dst.Clear()
		return err
	}
	pl := PostingList{Data: data}
	if err = pl.UnsafeBitmap(dst); err != nil {
		return err
	}
	if len(p.Headers) > 0 && !dst.IsEmpty() && uint64(dst.Maximum()) >= p.DocumentCount {
		maximum := dst.Maximum()
		dst.Clear()
		return corruptf("posting list %d references document %d of %d", idx, maximum, p.DocumentCount)
	}
	return nil
}

type PostingList struct {
	Data []byte
}

// UnsafeBitmap clears dst and decodes the posting list into it without
// copying: dst aliases Data, so it must not outlive the Storage and must not be
// mutated in place (Clone it first). A posting list that fails to decode
// leaves dst empty and returns an error wrapping ErrCorrupt.
func (l *PostingList) UnsafeBitmap(dst *roaring.Bitmap) (err error) {
	dst.Clear()
	if len(l.Data) == 0 {
		return nil
	}
	if _, err = dst.FromUnsafeBytes(l.Data); err != nil {
		dst.Clear()
		return corruptf("posting list does not decode: %v", err)
	}
	return nil
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
	PostingLists PostingLists
	// Token frequencies
	TokenFrequencies TokenFrequencies
	// Used to determine if the storage was already initialized or not
	Initialized bool
}

// FrequenciesOf returns the token's per-document frequencies, or nil if the
// token's range lies outside the storage (a damaged file).
func (s *Storage) FrequenciesOf(token *Token) (freqs TokenFrequencies) {
	freqs, _ = s.frequenciesOf(token)
	return freqs
}

func (s *Storage) frequenciesOf(token *Token) (freqs TokenFrequencies, err error) {
	end := token.FrequenciesIndex + token.FrequencyCount
	if end < token.FrequenciesIndex || end > uint64(len(s.TokenFrequencies)) {
		return nil, corruptf("token %q frequencies [%d, %d+%d) outside %d entries",
			token.Value.Bytes(), token.FrequenciesIndex, token.FrequenciesIndex, token.FrequencyCount, len(s.TokenFrequencies))
	}
	return s.TokenFrequencies[token.FrequenciesIndex:end:end], nil
}

func (s *Storage) ColdInitialize() {
	s.Version = CurrentVersion
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

// BuildFrom builds the storage in memory from a set of documents. Documents
// keep the order they are passed in; a document's position is its internal
// index.
//
// It returns ErrValueTooLong if a document id or a token is longer than
// MaxRawValueSize bytes (instead of storing it cut short, which would make it
// unsearchable and could merge distinct values), and ErrTooManyDocuments past
// the uint32 document limit. If building fails the storage is left unchanged.
//
// If the storage was loaded from a file it is closed only once the new
// storage is built, so docs may safely point into the old mapping (for
// example tokens taken from it to rebuild an index).
//
// The same token listed twice in one field of one document has its
// frequencies summed, and the same field listed twice in one document has its
// lengths summed, so the index stays consistent even if the caller does not
// deduplicate.
func (s *Storage) BuildFrom(docs ...*Document) (err error) {
	var built Storage
	if err = built.buildFrom(docs); err != nil {
		return err
	}
	if err = s.Close(); err != nil {
		return fmt.Errorf("failed to close previous storage: %w", err)
	}
	*s = built
	return nil
}

func (s *Storage) buildFrom(docs []*Document) (err error) {
	if uint64(len(docs)) > MaxDocuments {
		return fmt.Errorf("%w: %d documents", ErrTooManyDocuments, len(docs))
	}
	s.ColdInitialize()

	s.DocumentsIds = make([]DocumentId, len(docs))

	// Header and footer are always fixed size
	s.Size = uint64(HeaderSize) + uint64(FooterSize)

	type PostingData struct {
		Value []byte
		Freqs []TokenFrequencyEntry
	}
	type FieldAccumulator struct {
		TotalLength                uint64
		DocumentsCount             uint64
		DocumentsLengths           []DocumentLengthEntry
		Tokens                     *btree.Table[*PostingData]
		TotalTokenFrequenciesCount uint64
	}

	var postingListsCounter, tokensFreqsCounter uint64
	fieldsAccumulators := make(map[uint64]*FieldAccumulator)

	fieldAccPool := pool.New[FieldAccumulator](20)
	pdPool := pool.New[PostingData](20)

	for docIndex, doc := range docs {
		if doc == nil {
			return fmt.Errorf("document %d is nil", docIndex)
		}
		if doc.Id.Value.Truncated() {
			return fmt.Errorf("%w: document %d id is %d bytes", ErrValueTooLong, docIndex, doc.Id.Value.Size)
		}
		// Zero the unused tail of the slot so stale bytes from a reused
		// DocumentId never reach the file.
		id := doc.Id
		clear(id.Value.Data[id.Value.Size:])
		s.DocumentsIds[docIndex] = id
		docIdxUi32 := uint32(docIndex)

		// doc id header + doc id bytes
		s.Size += uint64(DocumentIdSize)

		for _, fieldDef := range doc.Fields {
			if fieldDef == nil {
				continue
			}
			fieldAccumulator, found := fieldsAccumulators[fieldDef.Hash]
			if !found {
				fieldAccumulator = fieldAccPool.Get()
				*fieldAccumulator = FieldAccumulator{
					Tokens: btree.NewTableOptions[*PostingData](
						btree.TableOptions[*PostingData]{
							Compare: func(pd1, pd2 *PostingData) int { return bytes.Compare(pd1.Value, pd2.Value) },
						},
					),
				}
				fieldsAccumulators[fieldDef.Hash] = fieldAccumulator

				// field header counted once per field
				s.Size += uint64(FieldHeaderSize)
			}

			if fieldDef.Length > 0 {
				fieldAccumulator.TotalLength += uint64(fieldDef.Length)
				lengths := fieldAccumulator.DocumentsLengths
				if n := len(lengths); n > 0 && lengths[n-1].Index == docIdxUi32 {
					// Same field twice in this document
					lengths[n-1].Length = saturatingAdd(lengths[n-1].Length, fieldDef.Length)
				} else {
					fieldAccumulator.DocumentsLengths = append(lengths, DocumentLengthEntry{
						Index:  docIdxUi32,
						Length: fieldDef.Length,
					})
					fieldAccumulator.DocumentsCount++

					// doc length entry
					s.Size += uint64(DocumentLengthEntrySize)
				}
			}

			var queryPd PostingData
			for _, tokenDef := range fieldDef.Tokens {
				if tokenDef == nil {
					continue
				}
				if len(tokenDef.Value) > MaxRawValueSize {
					return fmt.Errorf("%w: token of %d bytes in document %d", ErrValueTooLong, len(tokenDef.Value), docIndex)
				}
				queryPd.Value = tokenDef.Value
				pd, found := fieldAccumulator.Tokens.Lookup(&queryPd)
				if !found {
					postingListsCounter++
					pd = pdPool.Get()
					*pd = PostingData{Value: tokenDef.Value}
					fieldAccumulator.Tokens.Set(pd)
					s.Size += uint64(TokenSize)
				} else if n := len(pd.Freqs); n > 0 && pd.Freqs[n-1].DocumentIndex == docIdxUi32 {
					// Same token twice in this document's field
					pd.Freqs[n-1].Frequency = saturatingAdd(pd.Freqs[n-1].Frequency, tokenDef.Frequency)
					continue
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

	s.PostingLists.DocumentCount = uint64(len(docs))
	s.PostingLists.Entries = make([]PostingList, 0, postingListsCounter)
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

		var tokenIdx int
		for pd := range acc.Tokens.All() {
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

			pdBytes, err := workBitmap.MarshalBinary()
			if err != nil {
				return fmt.Errorf("failed to serialize posting list: %w", err)
			}

			s.Size += uint64(PostingListHeaderSize)
			s.Size += uint64(len(pdBytes))

			plIndex := uint64(s.PostingLists.Len())
			s.PostingLists.Entries = append(s.PostingLists.Entries, PostingList{Data: pdBytes})

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

		s.Fields[fieldHash] = field

		// Release this field's accumulator before moving on, bounding the live
		// working set to roughly a single field.
		acc.Tokens = nil
		delete(fieldsAccumulators, fieldHash)
	}
	return nil
}

// Largest number of documents a storage can hold: internal document indexes
// are uint32.
const MaxDocuments = 1<<32 - 1

func saturatingAdd(a, b uint32) uint32 {
	if sum := a + b; sum >= a {
		return sum
	}
	return 1<<32 - 1
}

// SaveTo writes the storage to name. The file is written under a temporary
// name in the same directory, synced, and only then renamed over name, so a
// failure or crash at any point leaves an existing file at name untouched, and
// processes that have the old file loaded keep reading the old contents.
func (s *Storage) SaveTo(name string) (err error) {
	return s.save(name, false)
}

// SaveToNew is SaveTo but never replaces an existing file: if name exists it
// returns an error matching os.ErrExist and writes nothing.
func (s *Storage) SaveToNew(name string) (err error) {
	return s.save(name, true)
}

func (s *Storage) save(name string, noReplace bool) (err error) {
	if !s.Initialized {
		// Cold initialize just to make sure we don't read an empty map
		s.ColdInitialize()
	}
	if uint64(len(s.DocumentsIds)) > MaxDocuments {
		return fmt.Errorf("%w: %d documents", ErrTooManyDocuments, len(s.DocumentsIds))
	}

	af, err := createAtomic(name, noReplace)
	if err != nil {
		return fmt.Errorf("failed to create dst file: %w", err)
	}
	defer af.Abort()
	file := af.File

	dst := bufio.NewWriterSize(file, 4<<20)

	// File Header
	header := Header{
		Magic:                     MagicNumber,
		Version:                   CurrentVersion,
		TotalDocuments:            uint32(len(s.DocumentsIds)),
		FieldCount:                uint64(len(s.Fields)),
		TotalPostingLists:         uint64(s.PostingLists.Len()),
		TotalPostingListsDataSize: uint64(s.PostingLists.DataSize()),
		TotalTokenFrequencies:     uint64(len(s.TokenFrequencies)),
	}
	_, err = dst.Write(pointers.UnsafeValueBytes(&header))
	if err != nil {
		return fmt.Errorf("failed to write header: %w", err)
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
		fieldHeader := FieldHeader{
			Hash:                  fieldHash,
			AvgDocumentLength:     field.AvgDocumentLength,
			TotalDocumentsLength:  field.TotalDocumentsLength,
			TokenCount:            uint64(len(field.Tokens)),
			TotalTokenFrequencies: field.TotalTokenFrequenciesCount,
			DocumentLengthCount:   uint64(len(field.DocumentLengths)),
		}
		_, err = dst.Write(pointers.UnsafeValueBytes(&fieldHeader))
		if err != nil {
			return fmt.Errorf("failed to write field header: %w", err)
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

	err = dst.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush remaining contents: %w", err)
	}

	// Write posting lists
	_, err = s.PostingLists.WriteToFile(file, dst)
	if err != nil {
		return fmt.Errorf("failed to write posting lists: %w", err)
	}

	if err = appendFooter(file); err != nil {
		return err
	}
	if err = af.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s: %w", name, err)
	}
	return nil
}

// Close unmaps a loaded storage and closes its file. Every slice the storage
// handed out (doc ids, tokens, frequencies, posting lists, bitmaps decoded
// with UnsafeBitmap) points into that mapping and must not be used afterwards;
// Close clears the storage so it cannot be used by mistake. On a storage that
// was built in memory and never loaded, Close does nothing.
func (s *Storage) Close() (err error) {
	if s.File == nil && s.Buffer == nil {
		return nil
	}
	var errs []error
	if s.Buffer != nil {
		if err := unix.Munmap(s.Buffer); err != nil {
			errs = append(errs, fmt.Errorf("failed to munmap buffer: %w", err))
		}
	}
	if s.File != nil {
		if err := s.File.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close file: %w", err))
		}
	}
	*s = Storage{}
	return errors.Join(errs...)
}

// Load maps the file read-only and points every table directly into the
// mapping. A storage that is already loaded is closed first.
//
// Load checks the file's structure: magic number, version, that every table
// fits in the file, and (format v2) that the footer is present. It does not
// read every byte, so it stays fast on large indexes; call Verify for the full
// checksum and cross-reference check. Errors caused by the file's contents
// wrap ErrCorrupt (or ErrUnsupportedVersion). On error the storage is left
// empty.
func (s *Storage) Load(name string) (err error) {
	if err = s.Close(); err != nil {
		return fmt.Errorf("failed to close previously loaded storage: %w", err)
	}
	*s = Storage{}

	file, err := os.Open(name)
	if err != nil {
		return fmt.Errorf("failed to open file handle: %w", err)
	}
	var buffer []byte
	defer func() {
		if err != nil {
			if buffer != nil {
				unix.Munmap(buffer)
			}
			file.Close()
			*s = Storage{}
		}
	}()

	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("failed to retrieve file information: %w", err)
	}

	size := info.Size()
	if size < int64(HeaderSize) {
		return corruptf("file is %d bytes, smaller than the %d byte header", size, HeaderSize)
	}

	buffer, err = unix.Mmap(int(file.Fd()), 0, int(size), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		buffer = nil
		return fmt.Errorf("failed mmap file: %w", err)
	}

	// Advisory only: failing (e.g. a kernel without transparent huge pages)
	// must not make the index unreadable.
	unix.Madvise(buffer, unix.MADV_SEQUENTIAL)
	unix.Madvise(buffer, unix.MADV_HUGEPAGE)

	err = s.mapTables(buffer)
	if err != nil {
		return err
	}
	s.File = file
	s.Buffer = buffer
	s.Initialized = true
	return nil
}

// mapSlice maps count records of T at the start of *buf and advances *buf
// past them. The size check divides instead of multiplying so a huge count
// from a damaged header cannot overflow past it.
func mapSlice[T any](buf *[]byte, count uint64, what string) (out []T, err error) {
	var zero T
	stride := uint64(unsafe.Sizeof(zero))
	if count > uint64(len(*buf))/stride {
		return nil, corruptf("%s: %d entries of %d bytes do not fit in the remaining %d bytes", what, count, stride, len(*buf))
	}
	if count == 0 {
		return nil, nil
	}
	out = unsafe.Slice((*T)(unsafe.Pointer(&(*buf)[0])), count)
	*buf = (*buf)[count*stride:]
	return out, nil
}

func (s *Storage) mapTables(buffer []byte) (err error) {
	// Zero copy access to the underlying buffer
	header := (*Header)(unsafe.Pointer(&buffer[0]))
	if header.Magic != MagicNumber {
		return corruptf("bad magic number %#x: not a textiplex index (or written on a machine of the other byte order)", header.Magic)
	}

	body := buffer
	switch header.Version {
	case VersionV1:
	case VersionV2:
		if uintptr(len(buffer)) < HeaderSize+FooterSize {
			return corruptf("file is %d bytes, too small for a version %d index", len(buffer), header.Version)
		}
		footer := (*Footer)(unsafe.Pointer(&buffer[uintptr(len(buffer))-FooterSize]))
		if footer.Magic != FooterMagic {
			return corruptf("footer missing: the file is truncated or was not completely written")
		}
		body = buffer[:uintptr(len(buffer))-FooterSize]
	default:
		return fmt.Errorf("%w: version %d (this build reads up to %d)", ErrUnsupportedVersion, header.Version, CurrentVersion)
	}

	rest := body[HeaderSize:]

	documentsIds, err := mapSlice[DocumentId](&rest, uint64(header.TotalDocuments), "document ids")
	if err != nil {
		return err
	}
	tokenFrequencies, err := mapSlice[TokenFrequencyEntry](&rest, header.TotalTokenFrequencies, "token frequencies")
	if err != nil {
		return err
	}

	// Bound the field count by the bytes available before allocating the map,
	// so a tiny file cannot request an enormous allocation.
	if header.FieldCount > uint64(len(rest))/uint64(FieldHeaderSize) {
		return corruptf("header claims %d fields but only %d bytes remain", header.FieldCount, len(rest))
	}
	fields := make(map[uint64]*Field, header.FieldCount)
	var fieldsPool = pool.New[Field](20)
	for fieldIdx := range header.FieldCount {
		fHeaders, err := mapSlice[FieldHeader](&rest, 1, "field header")
		if err != nil {
			return err
		}
		fHeader := &fHeaders[0]
		if _, duplicated := fields[fHeader.Hash]; duplicated {
			return corruptf("field %d: hash %#x appears twice", fieldIdx, fHeader.Hash)
		}

		field := fieldsPool.Get()
		field.AvgDocumentLength = fHeader.AvgDocumentLength
		field.TotalDocumentsLength = fHeader.TotalDocumentsLength
		field.TotalTokenFrequenciesCount = fHeader.TotalTokenFrequencies

		field.DocumentLengths, err = mapSlice[DocumentLengthEntry](&rest, fHeader.DocumentLengthCount, "field document lengths")
		if err != nil {
			return err
		}
		field.Tokens, err = mapSlice[Token](&rest, fHeader.TokenCount, "field tokens")
		if err != nil {
			return err
		}
		fields[fHeader.Hash] = field
	}

	plHeaders, err := mapSlice[PostingListHeader](&rest, header.TotalPostingLists, "posting list headers")
	if err != nil {
		return err
	}
	var reference []byte
	if len(plHeaders) > 0 {
		if header.TotalPostingListsDataSize > uint64(len(rest)) {
			return corruptf("posting list data: %d bytes do not fit in the remaining %d bytes", header.TotalPostingListsDataSize, len(rest))
		}
		// Consistency check only: each posting list's bounds are checked when
		// it is read, so a sum that wraps cannot cause an out-of-range access.
		// This loop touches every header and dominates Load time, so it is
		// kept as cheap as the original.
		var computed uint64
		for i := range plHeaders {
			computed += plHeaders[i].Length
		}
		if computed != header.TotalPostingListsDataSize {
			return corruptf("expecting a different amount of bytes for posting lists: Computed(%d) != Header(%d)", computed, header.TotalPostingListsDataSize)
		}
		n := header.TotalPostingListsDataSize
		reference = rest[:n:n]
		rest = rest[n:]
	}

	if header.Version >= VersionV2 && len(rest) != 0 {
		return corruptf("%d unexpected bytes before the footer", len(rest))
	}

	s.Version = header.Version
	s.Fields = fields
	s.DocumentsIds = documentsIds
	s.TokenFrequencies = tokenFrequencies
	s.PostingLists = PostingLists{
		Reference:     reference,
		Headers:       plHeaders,
		DocumentCount: uint64(header.TotalDocuments),
	}
	s.Size = uint64(len(buffer)) - uint64(len(rest))
	return nil
}
