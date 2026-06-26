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

type PendingPostingList struct {
	IndexA int64
	IndexB int64
}

type MergeContext struct {
	StorageA                             *Storage
	StorageB                             *Storage
	DstFile                              *os.File
	DstW                                 *bufio.Writer
	PostingListCursor, FrequenciesCursor uint64
	// Cached token frequencies
	TokenFrequencies                              []TokenFrequencyEntry
	PostingLists                                  []PendingPostingList
	Buffer                                        [8]byte
	CachedBitmapChunk                             [OffsetBitmapCachedSize]uint32
	DocumentOffset                                uint32
	ReusableBitmap, BitmapForPostingListRetrieval roaring.Bitmap
}

func (m *Merger) writeCollisionToken(ctx *MergeContext, fieldHash uint64, tokenA, tokenB *Token) (err error) {
	var finalToken Token
	switch {
	case tokenA != nil && tokenB != nil: // Equal
		finalToken = *tokenA
		finalToken.FrequencyCount = tokenA.FrequencyCount + tokenB.FrequencyCount
		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		ctx.PostingLists = append(ctx.PostingLists, PendingPostingList{
			IndexA: int64(tokenA.PostingListIndex),
			IndexB: int64(tokenB.PostingListIndex),
		})

		// Write the frequencies
		freqsA := ctx.StorageA.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
		ctx.TokenFrequencies = append(ctx.TokenFrequencies, freqsA...)
		freqsB := ctx.StorageB.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

		for index := range freqsB {
			freq := &freqsB[index]

			ctx.TokenFrequencies = append(ctx.TokenFrequencies, TokenFrequencyEntry{
				DocumentIndex: ctx.DocumentOffset + freq.DocumentIndex,
				Frequency:     freq.Frequency,
			})
		}
	case tokenA != nil:
		finalToken = *tokenA
		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		// Write the posting list
		ctx.PostingLists = append(ctx.PostingLists, PendingPostingList{
			IndexA: int64(tokenA.PostingListIndex),
			IndexB: -1,
		})

		// Write the frequencies
		freqs := ctx.StorageA.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
		ctx.TokenFrequencies = append(ctx.TokenFrequencies, freqs...)
	case tokenB != nil:
		finalToken = *tokenB
		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		// Write the posting list
		ctx.PostingLists = append(ctx.PostingLists, PendingPostingList{
			IndexA: -1,
			IndexB: int64(tokenB.PostingListIndex),
		})

		// Write the frequencies
		freqsB := ctx.StorageB.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

		for index := range freqsB {
			freq := &freqsB[index]

			ctx.TokenFrequencies = append(ctx.TokenFrequencies, TokenFrequencyEntry{
				DocumentIndex: ctx.DocumentOffset + freq.DocumentIndex,
				Frequency:     freq.Frequency,
			})
		}
	}

	_, err = ctx.DstW.Write(pointers.UnsafeSlice(&finalToken))
	if err != nil {
		return fmt.Errorf("failed to write Collision field token: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
	}
	return nil
}

// Merges storages B and B into the specified file
// Document ids should not collide in both storage
// otherwise undefined behavior will ocurr
func (m *Merger) Merge(name string, a, b *Storage) (err error) {
	var ctx = MergeContext{
		DocumentOffset:   uint32(len(a.DocumentsIds)),
		StorageA:         a,
		StorageB:         b,
		TokenFrequencies: make([]TokenFrequencyEntry, 0, len(a.TokenFrequencies)+len(b.TokenFrequencies)),
		PostingLists:     make([]PendingPostingList, 0, len(a.PostingLists)),
	}

	ctx.DstFile, err = os.Create(name)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer func() {
		ctx.DstFile.Close()
		if err != nil {
			os.Remove(name)
		}
	}()

	// Reserve the space for the header and the document ids inmediatly
	docIdsSize := (int64(DocumentIdSize) * (int64(len(a.DocumentsIds)) + int64(len(b.DocumentsIds))))
	reserveSize := int64(HeaderSize) + docIdsSize

	ctx.DstFile.Truncate(reserveSize)
	// Seek after the header to write document ids directly
	ctx.DstFile.Seek(int64(HeaderSize), 0)

	if len(a.DocumentsIds) > 0 {
		aDocsSlice := unsafe.Slice((*byte)(unsafe.Pointer(&a.DocumentsIds[0])), DocumentIdSize*uintptr(len(a.DocumentsIds)))
		_, err := ctx.DstFile.Write(aDocsSlice)
		if err != nil {
			return fmt.Errorf("failed to write storage A's document ids: %w", err)
		}
	}

	if len(b.DocumentsIds) > 0 {
		bDocsSlice := unsafe.Slice((*byte)(unsafe.Pointer(&b.DocumentsIds[0])), DocumentIdSize*uintptr(len(b.DocumentsIds)))
		_, err = ctx.DstFile.Write(bDocsSlice)
		if err != nil {
			return fmt.Errorf("failed to write storage B's document ids: %w", err)
		}
	}

	ctx.DstFile.Seek(reserveSize, io.SeekStart)

	ctx.DstW = bufio.NewWriterSize(ctx.DstFile, DefaultBufferedWriterSize)

	var fieldCollisions = make([]uint64, 0, len(a.Fields))

	// Phase 2, write A's only fields
	for fieldHash, field := range a.Fields {
		_, found := b.Fields[fieldHash]
		if found {
			// Do not process collision fields yet
			fieldCollisions = append(fieldCollisions, fieldHash)
			continue
		}

		// Write field header to temporary fields file
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write A's field hash: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&field.AvgDocumentLength))
		if err != nil {
			return fmt.Errorf("failed to write A's field avgdl: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&field.TotalDocumentsLength))
		if err != nil {
			return fmt.Errorf("failed to write A's field total document lengths: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(field.Tokens)))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's tokens length: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&field.TotalTokenFrequenciesCount))
		if err != nil {
			return fmt.Errorf("failed to write A's total frequencies count: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(field.DocumentLengths)))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's documents lengths: %w: %d", err, fieldHash)
		}
		if len(field.DocumentLengths) > 0 {
			fieldDocLengths := unsafe.Slice((*byte)(unsafe.Pointer(&field.DocumentLengths[0])), DocumentLengthEntrySize*uintptr(len(field.DocumentLengths)))
			_, err = ctx.DstW.Write(fieldDocLengths)
			if err != nil {
				return fmt.Errorf("failed to write storage Field Document length ids: %w", err)
			}
		}

		// Write posting lists
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			_, err = ctx.DstW.Write(pointers.UnsafeSlice(&token.FrequencyCount))
			if err != nil {
				return fmt.Errorf("failed to write A's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add posting list
			binary.NativeEndian.PutUint64(ctx.Buffer[:], ctx.PostingListCursor)
			ctx.PostingListCursor++
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write A's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add token frequency
			binary.NativeEndian.PutUint64(ctx.Buffer[:], ctx.FrequenciesCursor)
			ctx.FrequenciesCursor += token.FrequencyCount
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write A's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write the actual token
			_, err = ctx.DstW.Write(pointers.UnsafeSlice(&token.Value))
			if err != nil {
				return fmt.Errorf("failed to write A's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to the posting lists temporary file
			ctx.PostingLists = append(ctx.PostingLists, PendingPostingList{
				IndexA: int64(token.PostingListIndex),
				IndexB: -1,
			})

			// Write directly to frequencies temporary file
			freqs := a.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]
			ctx.TokenFrequencies = append(ctx.TokenFrequencies, freqs...)
		}
	}

	// Phase 3, write B's only fields
	for fieldHash, field := range b.Fields {
		_, found := a.Fields[fieldHash]
		if found {
			continue
		}

		// Write field header to temporary fields file
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&field.AvgDocumentLength))
		if err != nil {
			return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&field.TotalDocumentsLength))
		if err != nil {
			return fmt.Errorf("failed to write B's field total document lengths: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(field.Tokens)))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&field.TotalTokenFrequenciesCount))
		if err != nil {
			return fmt.Errorf("failed to write B's field total frequencies count: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(field.DocumentLengths)))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
		}

		for index := range field.DocumentLengths {
			dl := &field.DocumentLengths[index]

			binary.NativeEndian.PutUint32(ctx.Buffer[:], ctx.DocumentOffset+dl.Index)
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's document length index: %w: %d:%d", err, fieldHash, dl.Index)
			}

			_, err = ctx.DstW.Write(pointers.UnsafeSlice(&dl.Length))
			if err != nil {
				return fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, dl.Index)
			}
		}

		// Write posting lists
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			_, err = ctx.DstW.Write(pointers.UnsafeSlice(&token.FrequencyCount))
			if err != nil {
				return fmt.Errorf("failed to write B's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add posting list
			binary.NativeEndian.PutUint64(ctx.Buffer[:], ctx.PostingListCursor)
			ctx.PostingListCursor++
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to the posting lists temporary file
			ctx.PostingLists = append(ctx.PostingLists, PendingPostingList{
				IndexA: -1,
				IndexB: int64(token.PostingListIndex),
			})

			// Add token frequency
			binary.NativeEndian.PutUint64(ctx.Buffer[:], ctx.FrequenciesCursor)
			ctx.FrequenciesCursor += token.FrequencyCount
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to frequencies temporary file
			freqs := b.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

			for index := range freqs {
				freq := &freqs[index]

				ctx.TokenFrequencies = append(ctx.TokenFrequencies, TokenFrequencyEntry{
					DocumentIndex: ctx.DocumentOffset + freq.DocumentIndex,
					Frequency:     freq.Frequency,
				})
			}

			// Write the actual token
			_, err = ctx.DstW.Write(pointers.UnsafeSlice(&token.Value))
			if err != nil {
				return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
		}
	}

	// Phase 4, add collision fields
	for _, fieldHash := range fieldCollisions {
		fieldA := a.Fields[fieldHash]
		fieldB := b.Fields[fieldHash]

		var totalDocumentLengths = fieldA.TotalDocumentsLength + fieldB.TotalDocumentsLength
		var avgDocumentLength = float64(totalDocumentLengths) / float64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths))
		var tokensCount = CountTokensBetweenCollisionFields(fieldA, fieldB)

		// Write the field header inmediatly
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write collision field field hash: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&avgDocumentLength))
		if err != nil {
			return fmt.Errorf("failed to write collision field avgdl: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&totalDocumentLengths))
		if err != nil {
			return fmt.Errorf("failed to write collision field total document length: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&tokensCount))
		if err != nil {
			return fmt.Errorf("failed to write collision field tokens length: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], fieldA.TotalTokenFrequenciesCount+fieldB.TotalTokenFrequenciesCount)
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write collision field total tokens freqs count: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths)))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write collision field documents lengths: %w: %d", err, fieldHash)
		}

		for index := range fieldA.DocumentLengths {
			dl := &fieldA.DocumentLengths[index]
			_, err = ctx.DstW.Write(pointers.UnsafeSlice(dl))
			if err != nil {
				return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
			}
		}

		for index := range fieldB.DocumentLengths {
			dl := &fieldB.DocumentLengths[index]
			binary.NativeEndian.PutUint32(ctx.Buffer[:], ctx.DocumentOffset+dl.Index)
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
			}

			_, err = ctx.DstW.Write(pointers.UnsafeSlice(&dl.Length))
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
					err = m.writeCollisionToken(&ctx, fieldHash, nil, &fieldB.Tokens[bIdx])
					bIdx++
				case bIdx >= bLen:
					err = m.writeCollisionToken(&ctx, fieldHash, &fieldA.Tokens[aIdx], nil)
					aIdx++
				default:
					switch bytes.Compare(fieldA.Tokens[aIdx].Value.Bytes(), fieldB.Tokens[bIdx].Value.Bytes()) {
					case 0:
						err = m.writeCollisionToken(&ctx, fieldHash, &fieldA.Tokens[aIdx], &fieldB.Tokens[bIdx])
						aIdx++
						bIdx++
					case -1:
						err = m.writeCollisionToken(&ctx, fieldHash, &fieldA.Tokens[aIdx], nil)
						aIdx++
					default:
						err = m.writeCollisionToken(&ctx, fieldHash, nil, &fieldB.Tokens[bIdx])
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

	if len(ctx.TokenFrequencies) > 0 {
		freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&ctx.TokenFrequencies[0])), TokenFrequencyEntrySize*uintptr(len(ctx.TokenFrequencies)))
		_, err = ctx.DstW.Write(freqsBytes)
		if err != nil {
			return fmt.Errorf("failed to write token frequencies: %w", err)
		}
	}

	for i := range ctx.PostingLists {
		pending := &ctx.PostingLists[i]

		switch {
		case pending.IndexA != -1 && pending.IndexB != -1:
			rawA := &a.PostingLists[pending.IndexA]
			rawB := &b.PostingLists[pending.IndexB]

			ctx.ReusableBitmap.Clear()
			rawA.Bitmap(&ctx.BitmapForPostingListRetrieval)
			ctx.ReusableBitmap.Or(&ctx.BitmapForPostingListRetrieval)

			rawB.Bitmap(&ctx.BitmapForPostingListRetrieval)
			addOffsetFrom(&ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

			size := ctx.ReusableBitmap.GetSerializedSizeInBytes()
			binary.NativeEndian.PutUint64(ctx.Buffer[:], size)
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write length of posting list A&B: %w", err)
			}

			_, err = ctx.ReusableBitmap.WriteTo(ctx.DstW)
			if err != nil {
				return fmt.Errorf("failed to write contents of posting list A&B: %w", err)
			}
		case pending.IndexA != -1:
			rawA := &a.PostingLists[pending.IndexA]

			binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(rawA.Data)))
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write length of posting list A: %w", err)
			}

			_, err = ctx.DstW.Write(rawA.Data)
			if err != nil {
				return fmt.Errorf("failed to write contents of posting list A: %w", err)
			}
		case pending.IndexB != -1:
			rawB := &b.PostingLists[pending.IndexB]

			ctx.ReusableBitmap.Clear()
			rawB.Bitmap(&ctx.BitmapForPostingListRetrieval)

			addOffsetFrom(&ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

			size := ctx.ReusableBitmap.GetSerializedSizeInBytes()
			binary.NativeEndian.PutUint64(ctx.Buffer[:], size)
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write length of posting list A&B: %w", err)
			}

			_, err = ctx.ReusableBitmap.WriteTo(ctx.DstW)
			if err != nil {
				return fmt.Errorf("failed to write contents of posting list A&B: %w", err)
			}
		}
	}

	ctx.DstW.Flush()

	// File Header
	header := Header{
		Magic:                 MagicNumber,
		Version:               VersionV1,
		TotalDocuments:        uint32(len(a.DocumentsIds)) + uint32(len(b.DocumentsIds)),
		FieldCount:            (uint64(len(a.Fields)) + uint64(len(b.Fields))) - uint64(len(fieldCollisions)),
		TotalPostingLists:     ctx.PostingListCursor,
		TotalTokenFrequencies: ctx.FrequenciesCursor,
	}

	_, err = ctx.DstFile.WriteAt(pointers.UnsafeSlice(&header), 0)
	if err != nil {
		return fmt.Errorf("failed to write header: %w ", err)
	}

	return nil
}
