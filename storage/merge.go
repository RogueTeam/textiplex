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

type MergeContext struct {
	StorageA                                      *Storage
	StorageB                                      *Storage
	DstFile, PlFile                               *os.File
	DstW, PlW                                     *bufio.Writer
	PostingListCursor, FrequenciesCursor          uint64
	Buffer                                        [8]byte
	CachedBitmapChunk                             [OffsetBitmapCachedSize]uint32
	DocumentOffset                                uint32
	TokenFrequenciesOffset                        int64
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

		ctx.ReusableBitmap.Clear()

		ctx.StorageA.PostingLists[tokenA.PostingListIndex].Bitmap(&ctx.BitmapForPostingListRetrieval)
		ctx.ReusableBitmap.Or(&ctx.BitmapForPostingListRetrieval)

		ctx.StorageB.PostingLists[tokenB.PostingListIndex].Bitmap(&ctx.BitmapForPostingListRetrieval)

		addOffsetFrom(ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

		// Write the posting list
		size := ctx.ReusableBitmap.GetSerializedSizeInBytes()

		_, err := ctx.PlW.Write(pointers.UnsafeSlice(&size))
		if err != nil {
			return fmt.Errorf("failed to write Collision field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
		}

		_, err = ctx.ReusableBitmap.WriteTo(ctx.PlW)
		if err != nil {
			return fmt.Errorf("failed to write Collision field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
		}

		// Write the frequencies
		freqsA := ctx.StorageA.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
		freqsB := ctx.StorageB.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

		if len(freqsA) > 0 {
			freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqsA[0])), TokenFrequencyEntrySize*uintptr(len(freqsA)))
			tokFreqsDelta, err := ctx.DstFile.WriteAt(freqsBytes, ctx.TokenFrequenciesOffset)
			if err != nil {
				return fmt.Errorf("failed to write A' storage frequencies: %w", err)
			}
			ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)
		}

		for index := range freqsB {
			freq := &freqsB[index]

			binary.NativeEndian.PutUint32(ctx.Buffer[:], ctx.DocumentOffset+freq.DocumentIndex)
			tokFreqsDelta, err := ctx.DstFile.WriteAt(ctx.Buffer[:], ctx.TokenFrequenciesOffset)
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequency: %w: %d", err, fieldHash)
			}
			ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)

			tokFreqsDelta, err = ctx.DstFile.WriteAt(pointers.UnsafeSlice(&freq.Frequency), ctx.TokenFrequenciesOffset)
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequency: %w: %d", err, fieldHash)
			}
			ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)
		}
	case tokenA != nil:
		finalToken = *tokenA
		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		// Write the posting list
		postingList := &ctx.StorageA.PostingLists[tokenA.PostingListIndex]

		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(postingList.Data)))
		_, err = ctx.PlW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
		}
		_, err := ctx.PlW.Write(postingList.Data)
		if err != nil {
			return fmt.Errorf("failed to write A's field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
		}

		// Write the frequencies
		freqs := ctx.StorageA.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
		if len(freqs) > 0 {
			freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqs[0])), TokenFrequencyEntrySize*uintptr(len(freqs)))
			tokFreqsDelta, err := ctx.DstFile.WriteAt(freqsBytes, ctx.TokenFrequenciesOffset)
			if err != nil {
				return fmt.Errorf("failed to write A' storage frequencies: %w", err)
			}
			ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)
		}
	case tokenB != nil:
		finalToken = *tokenB
		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		// Write the posting list
		ctx.StorageB.PostingLists[tokenB.PostingListIndex].Bitmap(&ctx.BitmapForPostingListRetrieval)

		ctx.ReusableBitmap.Clear()

		addOffsetFrom(ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

		size := ctx.ReusableBitmap.GetSerializedSizeInBytes()

		_, err := ctx.PlW.Write(pointers.UnsafeSlice(&size))
		if err != nil {
			return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
		}
		_, err = ctx.ReusableBitmap.WriteTo(ctx.PlW)
		if err != nil {
			return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
		}

		// Write the frequencies
		freqs := ctx.StorageB.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

		for index := range freqs {
			freq := &freqs[index]

			binary.NativeEndian.PutUint32(ctx.Buffer[:], ctx.DocumentOffset+freq.DocumentIndex)
			tokFreqsDelta, err := ctx.DstFile.WriteAt(ctx.Buffer[:], ctx.TokenFrequenciesOffset)
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
			}
			ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)

			tokFreqsDelta, err = ctx.DstFile.WriteAt(pointers.UnsafeSlice(&freq.Frequency), ctx.TokenFrequenciesOffset)
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
			}
			ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)
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
		DocumentOffset: uint32(len(a.DocumentsIds)),
		StorageA:       a,
		StorageB:       b,
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

	// Temporary helper files
	ctx.PlFile, err = m.CreateTemp("posting-lists-*")
	if err != nil {
		return fmt.Errorf("failed to prepare field's posting list file: %w", err)
	}
	defer CloseAndRemove(ctx.PlFile)

	// Reserve the space for the header and the document ids inmediatly
	docIdsSize := (int64(DocumentIdSize) * (int64(len(a.DocumentsIds)) + int64(len(b.DocumentsIds))))
	tokenFreqsSize := (int64(TokenFrequencyEntrySize) * (int64(len(a.TokenFrequencies)) + int64(len(b.TokenFrequencies))))
	reserveSize := int64(HeaderSize) +
		docIdsSize +
		tokenFreqsSize

	ctx.DstFile.Truncate(reserveSize)
	// Seek after the header to write document ids directly
	ctx.DstFile.Seek(int64(HeaderSize), 0)

	ctx.TokenFrequenciesOffset = int64(HeaderSize) + docIdsSize

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
	ctx.PlW = bufio.NewWriterSize(ctx.PlFile, DefaultBufferedWriterSize)

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
			postingList := &a.PostingLists[token.PostingListIndex]

			binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(postingList.Data)))
			_, err = ctx.PlW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write A's field token posting list size: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			_, err = ctx.PlW.Write(postingList.Data)
			if err != nil {
				return fmt.Errorf("failed to write A's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to frequencies temporary file
			freqs := a.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]
			if len(freqs) > 0 {
				freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqs[0])), TokenFrequencyEntrySize*uintptr(len(freqs)))
				tokFreqsDelta, err := ctx.DstFile.WriteAt(freqsBytes, ctx.TokenFrequenciesOffset)
				if err != nil {
					return fmt.Errorf("failed to write storage frequencies: %w", err)
				}
				ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)
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
			b.PostingLists[token.PostingListIndex].Bitmap(&ctx.BitmapForPostingListRetrieval)

			ctx.ReusableBitmap.Clear()

			addOffsetFrom(&ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

			size := ctx.ReusableBitmap.GetSerializedSizeInBytes()

			_, err = ctx.PlW.Write(pointers.UnsafeSlice(&size))
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			_, err = ctx.ReusableBitmap.WriteTo(ctx.PlW)
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

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

				binary.NativeEndian.PutUint32(ctx.Buffer[:], ctx.DocumentOffset+freq.DocumentIndex)
				tokFreqsDelta, err := ctx.DstFile.WriteAt(ctx.Buffer[:], ctx.TokenFrequenciesOffset)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
				}
				ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)

				tokFreqsDelta, err = ctx.DstFile.WriteAt(pointers.UnsafeSlice(&freq.Frequency), ctx.TokenFrequenciesOffset)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
				}
				ctx.TokenFrequenciesOffset += int64(tokFreqsDelta)
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

	ctx.DstW.Flush()
	ctx.PlW.Flush()

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

	ctx.PlFile.Seek(0, io.SeekStart)
	_, err = ctx.DstFile.ReadFrom(ctx.PlFile)
	if err != nil {
		return fmt.Errorf("failed to append posting lists file: %w", err)
	}

	return nil
}
