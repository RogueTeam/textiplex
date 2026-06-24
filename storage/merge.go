package storage

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/RoaringBitmap/roaring"
	"github.com/RogueTeam/textiplex/pointers"
)

// Handles merges between storages
type Merger struct {
	TempDir string
}

func (m *Merger) CreateTemp(pattern string) (file *os.File, err error) {
	return os.CreateTemp(m.TempDir, pattern)
}

func CloseAndRemove(file *os.File) {
	file.Close()
	os.Remove(file.Name())
}

const DefaultBufferedWriterSize = 4 << 20

func CountTokensBetweenCollisionFields(fieldA, fieldB *Field) (count uint64) {
	aLen, bLen := len(fieldA.Tokens), len(fieldB.Tokens)
	for aIdx, bIdx := 0, 0; aIdx < aLen || bIdx < bLen; {
		count++
		switch {
		case aIdx >= aLen:
			bIdx++
		case bIdx >= bLen:
			aIdx++
		default:
			switch bytes.Compare(fieldA.Tokens[aIdx].Value.Bytes(), fieldB.Tokens[bIdx].Value.Bytes()) {
			case 0:
				aIdx++
				bIdx++
			case -1:
				aIdx++
			default:
				bIdx++
			}
		}
	}
	return count
}

func (m *Merger) writeCollisionToken(
	plCursor, freqsCursor *uint64,
	fieldHash uint64,
	buffer *[8]byte,
	cachedBitmapChunk *[OffsetBitmapCachedSize]uint32,
	docOffset uint32,
	reusableBitmap, bitmapForPostingListRetrieval *roaring.Bitmap,
	a, b *Storage,
	dstW, plW, tokFreqsW *bufio.Writer,
	tokenA, tokenB *Token,
) (err error) {
	var finalToken Token
	switch {
	case tokenA != nil && tokenB != nil: // Equal
		finalToken = *tokenA
		finalToken.FrequencyCount = tokenA.FrequencyCount + tokenB.FrequencyCount
		finalToken.PostingListIndex = *plCursor
		*plCursor++
		finalToken.FrequenciesIndex = *freqsCursor
		*freqsCursor += finalToken.FrequencyCount

		reusableBitmap.Clear()

		a.PostingLists[tokenA.PostingListIndex].Bitmap(bitmapForPostingListRetrieval)
		reusableBitmap.Or(bitmapForPostingListRetrieval)

		b.PostingLists[tokenB.PostingListIndex].Bitmap(bitmapForPostingListRetrieval)

		addOffsetFrom(reusableBitmap, bitmapForPostingListRetrieval, cachedBitmapChunk, docOffset)

		// Write the posting list
		size := reusableBitmap.GetSerializedSizeInBytes()

		_, err := plW.Write(pointers.UnsafeSlice(&size))
		if err != nil {
			return fmt.Errorf("failed to write Collision field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
		}

		_, err = reusableBitmap.WriteTo(plW)
		if err != nil {
			return fmt.Errorf("failed to write Collision field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
		}

		// Write the frequencies
		freqsA := a.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
		freqsB := b.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

		if len(freqsA) > 0 {
			freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqsA[0])), TokenFrequencyEntrySize*uintptr(len(freqsA)))
			_, err = tokFreqsW.Write(freqsBytes)
			if err != nil {
				return fmt.Errorf("failed to write A' storage frequencies: %w", err)
			}
		}

		for index := range freqsB {
			freq := &freqsB[index]

			binary.NativeEndian.PutUint32(buffer[:], docOffset+freq.DocumentIndex)
			_, err = tokFreqsW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequency: %w: %d", err, fieldHash)
			}

			_, err = tokFreqsW.Write(pointers.UnsafeSlice(&freq.Frequency))
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequency: %w: %d", err, fieldHash)
			}
		}
	case tokenA != nil:
		finalToken = *tokenA
		finalToken.PostingListIndex = *plCursor
		*plCursor++
		finalToken.FrequenciesIndex = *freqsCursor
		*freqsCursor += finalToken.FrequencyCount

		// Write the posting list
		postingList := &a.PostingLists[tokenA.PostingListIndex]

		binary.NativeEndian.PutUint64(buffer[:], uint64(len(postingList.Data)))
		_, err = plW.Write(buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
		}
		_, err := plW.Write(postingList.Data)
		if err != nil {
			return fmt.Errorf("failed to write A's field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
		}

		// Write the frequencies
		freqs := a.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
		if len(freqs) > 0 {
			freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqs[0])), TokenFrequencyEntrySize*uintptr(len(freqs)))
			_, err = tokFreqsW.Write(freqsBytes)
			if err != nil {
				return fmt.Errorf("failed to write A' storage frequencies: %w", err)
			}
		}
	case tokenB != nil:
		finalToken = *tokenB
		finalToken.PostingListIndex = *plCursor
		*plCursor++
		finalToken.FrequenciesIndex = *freqsCursor
		*freqsCursor += finalToken.FrequencyCount

		// Write the posting list
		b.PostingLists[tokenB.PostingListIndex].Bitmap(bitmapForPostingListRetrieval)

		reusableBitmap.Clear()

		addOffsetFrom(reusableBitmap, bitmapForPostingListRetrieval, cachedBitmapChunk, docOffset)

		size := reusableBitmap.GetSerializedSizeInBytes()

		_, err := plW.Write(pointers.UnsafeSlice(&size))
		if err != nil {
			return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
		}
		_, err = reusableBitmap.WriteTo(plW)
		if err != nil {
			return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
		}

		// Write the frequencies
		freqs := b.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

		for index := range freqs {
			freq := &freqs[index]

			binary.NativeEndian.PutUint32(buffer[:], docOffset+freq.DocumentIndex)
			_, err = tokFreqsW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
			}

			_, err = tokFreqsW.Write(pointers.UnsafeSlice(&freq.Frequency))
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
			}
		}
	}

	_, err = dstW.Write(pointers.UnsafeSlice(&finalToken))
	if err != nil {
		return fmt.Errorf("failed to write Collision field token: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
	}
	return nil
}

// Merges storages B and B into the specified file
// Document ids should not collide in both storage
// otherwise undefined behavior will ocurr
func (m *Merger) Merge(name string, a, b *Storage) (err error) {
	var buffer [8]byte
	var cachedBitmapChunk [OffsetBitmapCachedSize]uint32
	var bitmapForPostingListRetrieval roaring.Bitmap
	var reusableBitmap roaring.Bitmap

	docOffset := uint32(len(a.DocumentsIds))

	dstFile, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		dstFile.Close()
		if err != nil {
			os.Remove(name)
		}
	}()

	// Temporary helper files
	plFile, err := m.CreateTemp("posting-lists-*")
	if err != nil {
		return fmt.Errorf("failed to prepare field's posting list file: %w", err)
	}
	defer CloseAndRemove(plFile)

	tokFreqsFile, err := m.CreateTemp("field-token-freqs-*")
	if err != nil {
		return fmt.Errorf("failed to prepare field's token frequencies file: %w", err)
	}
	defer CloseAndRemove(tokFreqsFile)

	// Reserve the space for the header and the document ids inmediatly
	dstFile.Truncate(int64(HeaderSize) + (int64(DocumentIdSize) * (int64(len(a.DocumentsIds)) + int64(len(b.DocumentsIds)))))
	// Seek after the header to write document ids directly
	dstFile.Seek(int64(HeaderSize), 0)

	if len(a.DocumentsIds) > 0 {
		aDocsSlice := unsafe.Slice((*byte)(unsafe.Pointer(&a.DocumentsIds[0])), DocumentIdSize*uintptr(len(a.DocumentsIds)))
		_, err := dstFile.Write(aDocsSlice)
		if err != nil {
			return fmt.Errorf("failed to write storage A's document ids: %w", err)
		}
	}

	if len(b.DocumentsIds) > 0 {
		bDocsSlice := unsafe.Slice((*byte)(unsafe.Pointer(&b.DocumentsIds[0])), DocumentIdSize*uintptr(len(b.DocumentsIds)))
		_, err = dstFile.Write(bDocsSlice)
		if err != nil {
			return fmt.Errorf("failed to write storage B's document ids: %w", err)
		}
	}

	dstW := bufio.NewWriterSize(dstFile, DefaultBufferedWriterSize)
	plW := bufio.NewWriterSize(plFile, DefaultBufferedWriterSize)
	tokFreqsW := bufio.NewWriterSize(tokFreqsFile, DefaultBufferedWriterSize)

	var fieldCollisions = make([]uint64, 0, len(a.Fields))
	var plCursor, freqsCursor uint64

	// Phase 2, write A's only fields
	for fieldHash, field := range a.Fields {
		_, found := b.Fields[fieldHash]
		if found {
			// Do not process collision fields yet
			fieldCollisions = append(fieldCollisions, fieldHash)
			continue
		}

		// Write field header to temporary fields file
		_, err = dstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write A's field hash: %w: %d", err, fieldHash)
		}
		_, err = dstW.Write(pointers.UnsafeSlice(&field.AvgDocumentLength))
		if err != nil {
			return fmt.Errorf("failed to write A's field avgdl: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.Tokens)))
		_, err = dstW.Write(buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's tokens length: %w: %d", err, fieldHash)
		}
		_, err = dstW.Write(pointers.UnsafeSlice(&field.TotalTokenFrequenciesCount))
		if err != nil {
			return fmt.Errorf("failed to write A's total frequencies count: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.DocumentLengths)))
		_, err = dstW.Write(buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's documents lengths: %w: %d", err, fieldHash)
		}
		if len(field.DocumentLengths) > 0 {
			fieldDocLengths := unsafe.Slice((*byte)(unsafe.Pointer(&field.DocumentLengths[0])), DocumentLengthEntrySize*uintptr(len(field.DocumentLengths)))
			_, err = dstW.Write(fieldDocLengths)
			if err != nil {
				return fmt.Errorf("failed to write storage Field Document length ids: %w", err)
			}
		}

		// Write posting lists
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			_, err = dstW.Write(pointers.UnsafeSlice(&token.FrequencyCount))
			if err != nil {
				return fmt.Errorf("failed to write A's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add posting list
			binary.NativeEndian.PutUint64(buffer[:], plCursor)
			plCursor++
			_, err = dstW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write A's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add token frequency
			binary.NativeEndian.PutUint64(buffer[:], freqsCursor)
			freqsCursor += token.FrequencyCount
			_, err = dstW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write A's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write the actual token
			_, err = dstW.Write(pointers.UnsafeSlice(&token.Value))
			if err != nil {
				return fmt.Errorf("failed to write A's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to the posting lists temporary file
			postingList := &a.PostingLists[token.PostingListIndex]

			binary.NativeEndian.PutUint64(buffer[:], uint64(len(postingList.Data)))
			_, err = plW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write A's field token posting list size: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			_, err = plW.Write(postingList.Data)
			if err != nil {
				return fmt.Errorf("failed to write A's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to frequencies temporary file
			freqs := a.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]
			if len(freqs) > 0 {
				freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqs[0])), TokenFrequencyEntrySize*uintptr(len(freqs)))
				_, err = tokFreqsW.Write(freqsBytes)
				if err != nil {
					return fmt.Errorf("failed to write storage frequencies: %w", err)
				}
			}
		}
	}

	// Phase 3, write B's only fields
	for fieldHash, field := range b.Fields {
		_, found := a.Fields[fieldHash]
		if found {
			continue
		}

		// Write field header to temporary fields file
		_, err = dstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}
		_, err = dstW.Write(pointers.UnsafeSlice(&field.AvgDocumentLength))
		if err != nil {
			return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.Tokens)))
		_, err = dstW.Write(buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
		}
		_, err = dstW.Write(pointers.UnsafeSlice(&field.TotalTokenFrequenciesCount))
		if err != nil {
			return fmt.Errorf("failed to write B's field total frequencies count: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.DocumentLengths)))
		_, err = dstW.Write(buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
		}

		for index := range field.DocumentLengths {
			dl := &field.DocumentLengths[index]

			binary.NativeEndian.PutUint32(buffer[:], docOffset+dl.Index)
			_, err = dstW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's document length index: %w: %d:%d", err, fieldHash, dl.Index)
			}

			_, err = dstW.Write(pointers.UnsafeSlice(&dl.Length))
			if err != nil {
				return fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, dl.Index)
			}
		}

		// Write posting lists
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			_, err = dstW.Write(pointers.UnsafeSlice(&token.FrequencyCount))
			if err != nil {
				return fmt.Errorf("failed to write B's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add posting list
			binary.NativeEndian.PutUint64(buffer[:], plCursor)
			plCursor++
			_, err = dstW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to the posting lists temporary file
			b.PostingLists[token.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)

			reusableBitmap.Clear()

			addOffsetFrom(&reusableBitmap, &bitmapForPostingListRetrieval, &cachedBitmapChunk, docOffset)

			size := reusableBitmap.GetSerializedSizeInBytes()

			_, err = plW.Write(pointers.UnsafeSlice(&size))
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			_, err = reusableBitmap.WriteTo(plW)
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add token frequency
			binary.NativeEndian.PutUint64(buffer[:], freqsCursor)
			freqsCursor += token.FrequencyCount
			_, err = dstW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to frequencies temporary file
			freqs := b.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

			for index := range freqs {
				freq := &freqs[index]

				binary.NativeEndian.PutUint32(buffer[:], docOffset+freq.DocumentIndex)
				_, err = tokFreqsW.Write(buffer[:])
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
				}

				_, err = tokFreqsW.Write(pointers.UnsafeSlice(&freq.Frequency))
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
				}
			}

			// Write the actual token
			_, err = dstW.Write(pointers.UnsafeSlice(&token.Value))
			if err != nil {
				return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
		}
	}

	// Phase 4, add collision fields
	for _, fieldHash := range fieldCollisions {
		fieldA := a.Fields[fieldHash]
		fieldB := b.Fields[fieldHash]

		var totalDocumentLengths uint64
		for i := range fieldA.DocumentLengths {
			totalDocumentLengths += fieldA.DocumentLengths[i].Length
		}
		for i := range fieldB.DocumentLengths {
			totalDocumentLengths += fieldB.DocumentLengths[i].Length
		}
		var avgDocumentLength = float64(totalDocumentLengths) / float64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths))
		var tokensCount = CountTokensBetweenCollisionFields(fieldA, fieldB)

		// Write the field header inmediatly
		_, err = dstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write collision field field hash: %w: %d", err, fieldHash)
		}
		_, err = dstW.Write(pointers.UnsafeSlice(&avgDocumentLength))
		if err != nil {
			return fmt.Errorf("failed to write collision field avgdl: %w: %d", err, fieldHash)
		}
		_, err = dstW.Write(pointers.UnsafeSlice(&tokensCount))
		if err != nil {
			return fmt.Errorf("failed to write collision field tokens length: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(buffer[:], fieldA.TotalTokenFrequenciesCount+fieldB.TotalTokenFrequenciesCount)
		_, err = dstW.Write(buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write collision field total tokens freqs count: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(buffer[:], uint64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths)))
		_, err = dstW.Write(buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write collision field documents lengths: %w: %d", err, fieldHash)
		}

		for index := range fieldA.DocumentLengths {
			dl := &fieldA.DocumentLengths[index]
			_, err = dstW.Write(pointers.UnsafeSlice(dl))
			if err != nil {
				return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
			}
		}

		for index := range fieldB.DocumentLengths {
			dl := &fieldB.DocumentLengths[index]
			binary.NativeEndian.PutUint32(buffer[:], docOffset+dl.Index)
			_, err = dstW.Write(buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
			}

			_, err = dstW.Write(pointers.UnsafeSlice(&dl.Length))
			if err != nil {
				return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
			}
		}

		//

		err = func() (err error) {
			aLen, bLen := len(fieldA.Tokens), len(fieldB.Tokens)
			for aIdx, bIdx := 0, 0; aIdx < aLen || bIdx < bLen; {
				switch {
				case aIdx >= aLen:
					err = m.writeCollisionToken(
						&plCursor, &freqsCursor,
						fieldHash, &buffer, &cachedBitmapChunk, docOffset,
						&reusableBitmap, &bitmapForPostingListRetrieval,
						a, b,
						dstW, plW, tokFreqsW,
						nil, &fieldB.Tokens[bIdx],
					)
					bIdx++
				case bIdx >= bLen:
					err = m.writeCollisionToken(
						&plCursor, &freqsCursor,
						fieldHash, &buffer, &cachedBitmapChunk, docOffset,
						&reusableBitmap, &bitmapForPostingListRetrieval,
						a, b,
						dstW, plW, tokFreqsW,
						&fieldA.Tokens[aIdx], nil,
					)
					aIdx++
				default:
					switch bytes.Compare(fieldA.Tokens[aIdx].Value.Bytes(), fieldB.Tokens[bIdx].Value.Bytes()) {
					case 0:
						err = m.writeCollisionToken(
							&plCursor, &freqsCursor,
							fieldHash, &buffer, &cachedBitmapChunk, docOffset,
							&reusableBitmap, &bitmapForPostingListRetrieval,
							a, b,
							dstW, plW, tokFreqsW,
							&fieldA.Tokens[aIdx], &fieldB.Tokens[bIdx],
						)
						aIdx++
						bIdx++
					case -1:
						err = m.writeCollisionToken(
							&plCursor, &freqsCursor,
							fieldHash, &buffer, &cachedBitmapChunk, docOffset,
							&reusableBitmap, &bitmapForPostingListRetrieval,
							a, b,
							dstW, plW, tokFreqsW,
							&fieldA.Tokens[aIdx], nil,
						)
						aIdx++
					default:
						err = m.writeCollisionToken(
							&plCursor, &freqsCursor,
							fieldHash, &buffer, &cachedBitmapChunk, docOffset,
							&reusableBitmap, &bitmapForPostingListRetrieval,
							a, b,
							dstW, plW, tokFreqsW,
							nil, &fieldB.Tokens[bIdx],
						)
						bIdx++
					}
				}
				if err != nil {
					return fmt.Errorf("failed to write collision token: %w: %d", err, fieldHash)
				}
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("failed to handle collision field: %d: %w", fieldHash, err)
		}
	}

	// Phase 5, Assembly everything

	dstW.Flush()
	plW.Flush()
	tokFreqsW.Flush()

	// File Header
	header := Header{
		Magic:                 MagicNumber,
		Version:               VersionV1,
		TotalDocuments:        uint32(len(a.DocumentsIds)) + uint32(len(b.DocumentsIds)),
		FieldCount:            (uint64(len(a.Fields)) + uint64(len(b.Fields))) - uint64(len(fieldCollisions)),
		TotalPostingLists:     plCursor,
		TotalTokenFrequencies: freqsCursor,
	}
	dstFile.Seek(0, io.SeekStart)

	_, err = dstFile.Write(pointers.UnsafeSlice(&header))
	if err != nil {
		return fmt.Errorf("failed to write header: %w ", err)
	}

	dstFile.Seek(0, io.SeekEnd)

	plFile.Seek(0, io.SeekStart)
	_, err = dstFile.ReadFrom(plFile)
	if err != nil {
		return fmt.Errorf("failed to append posting lists file: %w", err)
	}

	tokFreqsFile.Seek(0, io.SeekStart)
	_, err = dstFile.ReadFrom(tokFreqsFile)
	if err != nil {
		return fmt.Errorf("failed to append token frequencies file file: %w", err)
	}

	return nil
}
