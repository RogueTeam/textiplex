package storage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"runtime"
	"time"
	"unsafe"

	"github.com/RoaringBitmap/roaring"
	"github.com/RogueTeam/textiplex/pointers"
	"github.com/shirou/gopsutil/v4/mem"
	"golang.org/x/sys/unix"
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

func (m *Merger) preparationPass(ctx *MergeContext) (err error) {
	ctx.FieldsOrder.A = make([]uint64, 0, len(ctx.StorageA.Fields))

	for fieldHash, field := range ctx.StorageA.Fields {
		_, found := ctx.StorageB.Fields[fieldHash]
		if found {
			// Do not process collision fields yet
			ctx.FieldsOrder.Collision = append(ctx.FieldsOrder.Collision, fieldHash)
			continue
		}
		ctx.FieldsOrder.A = append(ctx.FieldsOrder.A, fieldHash)

		// Write posting lists
		ctx.PostingListCount += uint64(len(field.Tokens))
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			freqs := ctx.StorageA.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

			freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqs[0])), TokenFrequencyEntrySize*uintptr(len(freqs)))
			_, err = ctx.DstW.Write(freqsBytes)
			if err != nil {
				return fmt.Errorf("failed to write A's token frequencies: %w", err)
			}
		}
	}

	ctx.FieldsOrder.B = make([]uint64, 0, len(ctx.StorageB.Fields)-len(ctx.FieldsOrder.Collision))
	// Phase 3, write B's only fields
	for fieldHash, field := range ctx.StorageB.Fields {
		_, found := ctx.StorageA.Fields[fieldHash]
		if found {
			continue
		}
		ctx.FieldsOrder.B = append(ctx.FieldsOrder.B, fieldHash)

		// Write posting lists
		ctx.PostingListCount += uint64(len(field.Tokens))
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			// Write directly to frequencies temporary file
			freqs := ctx.StorageB.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

			for index := range freqs {
				freq := &freqs[index]

				binary.NativeEndian.PutUint32(ctx.Buffer[:4], ctx.DocumentOffset+freq.DocumentIndex)
				_, err = ctx.DstW.Write(ctx.Buffer[:4])
				if err != nil {
					return fmt.Errorf("failed to write B's token frequency index: %w", err)
				}

				_, err = ctx.DstW.Write(pointers.UnsafeSlice(&freq.Frequency))
				if err != nil {
					return fmt.Errorf("failed to write B's token frequency: %w", err)
				}
			}
		}
	}

	// Phase 4, add collision fields
	for _, fieldHash := range ctx.FieldsOrder.Collision {
		fieldA := ctx.StorageA.Fields[fieldHash]
		fieldB := ctx.StorageB.Fields[fieldHash]

		//

		err = func() (err error) {
			aLen, bLen := len(fieldA.Tokens), len(fieldB.Tokens)
			for aIdx, bIdx := 0, 0; aIdx < aLen || bIdx < bLen; {
				ctx.PostingListCount++
				switch {
				case aIdx >= aLen:
					tokenB := &fieldB.Tokens[bIdx]

					freqsB := ctx.StorageB.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]
					for index := range freqsB {
						freq := &freqsB[index]

						binary.NativeEndian.PutUint32(ctx.Buffer[:4], ctx.DocumentOffset+freq.DocumentIndex)
						_, err = ctx.DstW.Write(ctx.Buffer[:4])
						if err != nil {
							return fmt.Errorf("failed to write B's token frequency index: %w", err)
						}

						_, err = ctx.DstW.Write(pointers.UnsafeSlice(&freq.Frequency))
						if err != nil {
							return fmt.Errorf("failed to write B's token frequency: %w", err)
						}
					}

					bIdx++
				case bIdx >= bLen:
					tokenA := &fieldA.Tokens[aIdx]

					freqsA := ctx.StorageA.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]

					freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqsA[0])), TokenFrequencyEntrySize*uintptr(len(freqsA)))
					_, err = ctx.DstW.Write(freqsBytes)
					if err != nil {
						return fmt.Errorf("failed to write A's token frequencies: %w", err)
					}

					aIdx++
				default:
					switch bytes.Compare(fieldA.Tokens[aIdx].Value.Bytes(), fieldB.Tokens[bIdx].Value.Bytes()) {
					case 0:
						tokenA := &fieldA.Tokens[aIdx]
						tokenB := &fieldB.Tokens[bIdx]

						freqsA := ctx.StorageA.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]

						freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqsA[0])), TokenFrequencyEntrySize*uintptr(len(freqsA)))
						_, err = ctx.DstW.Write(freqsBytes)
						if err != nil {
							return fmt.Errorf("failed to write A's token frequencies: %w", err)
						}

						freqsB := ctx.StorageB.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]
						for index := range freqsB {
							freq := &freqsB[index]

							binary.NativeEndian.PutUint32(ctx.Buffer[:4], ctx.DocumentOffset+freq.DocumentIndex)
							_, err = ctx.DstW.Write(ctx.Buffer[:4])
							if err != nil {
								return fmt.Errorf("failed to write B's token frequency index: %w", err)
							}

							_, err = ctx.DstW.Write(pointers.UnsafeSlice(&freq.Frequency))
							if err != nil {
								return fmt.Errorf("failed to write B's token frequency: %w", err)
							}
						}

						aIdx++
						bIdx++
					case -1:
						tokenA := &fieldA.Tokens[aIdx]

						freqsA := ctx.StorageA.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]

						freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqsA[0])), TokenFrequencyEntrySize*uintptr(len(freqsA)))
						_, err = ctx.DstW.Write(freqsBytes)
						if err != nil {
							return fmt.Errorf("failed to write A's token frequencies: %w", err)
						}

						aIdx++
					default:
						tokenB := &fieldB.Tokens[bIdx]

						freqsB := ctx.StorageB.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]
						for index := range freqsB {
							freq := &freqsB[index]

							binary.NativeEndian.PutUint32(ctx.Buffer[:4], ctx.DocumentOffset+freq.DocumentIndex)
							_, err = ctx.DstW.Write(ctx.Buffer[:4])
							if err != nil {
								return fmt.Errorf("failed to write B's token frequency index: %w", err)
							}

							_, err = ctx.DstW.Write(pointers.UnsafeSlice(&freq.Frequency))
							if err != nil {
								return fmt.Errorf("failed to write B's token frequency: %w", err)
							}
						}

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

	return nil
}

type PendingPostingList struct {
	IndexA int64
	IndexB int64
}

const PendingPostingListSize = unsafe.Sizeof(PendingPostingList{})

type FieldsOrder struct {
	A, B, Collision []uint64
}

func (o *FieldsOrder) Count() (count uint64) {
	return uint64(len(o.A)) + uint64(len(o.B)) + uint64(len(o.Collision))
}

type MergeContext struct {
	StorageA                             *Storage
	StorageB                             *Storage
	DstFile                              *os.File
	DstW                                 *bufio.Writer
	PostingListCursor, FrequenciesCursor uint64
	// Cached token frequencies
	PostingListCount                              uint64
	PostingLists                                  []PendingPostingList
	Buffer                                        [8]byte
	CachedBitmapChunk                             [OffsetBitmapCachedSize]uint32
	DocumentOffset                                uint32
	ReusableBitmap, BitmapForPostingListRetrieval roaring.Bitmap
	// Iteration order
	FieldsOrder FieldsOrder
}

func (m *Merger) writeCollisionToken(ctx *MergeContext, fieldA, fieldB *Field, fieldHash uint64, tokenA, tokenB *Token) (err error) {
	var finalToken Token
	switch {
	case tokenA != nil && tokenB != nil: // Equal
		finalToken = *tokenA
		finalToken.FrequencyCount = tokenA.FrequencyCount + tokenB.FrequencyCount
		finalToken.Idf = InverseDocumentFrequency(
			uint64(len(fieldA.DocumentLengths))+uint64(len(fieldB.DocumentLengths)),
			finalToken.FrequencyCount,
		)

		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		ctx.PostingLists = append(ctx.PostingLists, PendingPostingList{
			IndexA: int64(tokenA.PostingListIndex),
			IndexB: int64(tokenB.PostingListIndex),
		})
	case tokenA != nil:
		finalToken = *tokenA
		finalToken.Idf = InverseDocumentFrequency(
			uint64(len(fieldA.DocumentLengths)),
			finalToken.FrequencyCount,
		)

		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		// Write the posting list
		ctx.PostingLists = append(ctx.PostingLists, PendingPostingList{
			IndexA: int64(tokenA.PostingListIndex),
			IndexB: -1,
		})
	case tokenB != nil:
		finalToken = *tokenB
		finalToken.Idf = InverseDocumentFrequency(
			uint64(len(fieldB.DocumentLengths)),
			finalToken.FrequencyCount,
		)

		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		// Write the posting list
		ctx.PostingLists = append(ctx.PostingLists, PendingPostingList{
			IndexA: -1,
			IndexB: int64(tokenB.PostingListIndex),
		})
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

	err = m.preparationPass(&ctx)
	if err != nil {
		return fmt.Errorf("failed to write token frequencies: %w", err)
	}

	plSize := PendingPostingListSize * uintptr(ctx.PostingListCount)
	necessarySize := int64(plSize)

	var maxInMemoryPostingList int64
	memCtx, cancel := context.WithTimeout(context.TODO(), time.Second)
	defer cancel()
	v, _ := mem.VirtualMemoryWithContext(memCtx)
	if v != nil {
		maxInMemoryPostingList = int64(v.Available / uint64(runtime.NumCPU()))
	}

	var memFile *os.File
	var mmapMemFile []byte
	if necessarySize < maxInMemoryPostingList {
		ctx.PostingLists = make([]PendingPostingList, 0, ctx.PostingListCount)
		defer func() { ctx.PostingLists = nil }() // Drop the reference as soon as posible
	} else {
		memFile, err = m.CreateTemp("*.tmp")
		if err != nil {
			return fmt.Errorf("failed to create temporary file: %w", err)
		}
		defer CloseAndRemove(memFile)

		err = memFile.Truncate(necessarySize)
		if err != nil {
			return fmt.Errorf("failed to truncate temporary memfile: %w", err)
		}

		mmapMemFile, err = unix.Mmap(
			int(memFile.Fd()),
			0,
			int(necessarySize),
			unix.PROT_READ|unix.PROT_WRITE,
			unix.MAP_SHARED,
		)
		if err != nil {
			return fmt.Errorf("failed to mmap file: %w", err)
		}
		defer unix.Munmap(mmapMemFile)

		err = unix.Madvise(mmapMemFile, unix.MADV_SEQUENTIAL)
		if err != nil {
			return fmt.Errorf("failed to madvise sequential: %w", err)
		}
		err = unix.Madvise(mmapMemFile, unix.MADV_HUGEPAGE)
		if err != nil {
			return fmt.Errorf("failed to madvise huge page: %w", err)
		}

		if ctx.PostingListCount > 0 {
			ptr := (*PendingPostingList)(unsafe.Pointer(&mmapMemFile[0]))
			ctx.PostingLists = unsafe.Slice(ptr, ctx.PostingListCount)[:0]
		}
	}

	// Phase 2, write A's only fields
	for _, fieldHash := range ctx.FieldsOrder.A {
		field := ctx.StorageA.Fields[fieldHash]

		// Write field header to temporary fields file
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write A's field hash: %w: %d", err, fieldHash)
		}
		copy(ctx.Buffer[:], pointers.UnsafeSlice(&field.AvgDocumentLength))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
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

			copy(ctx.Buffer[:], pointers.UnsafeSlice(&token.Idf))
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write A's field token idf: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
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
		}
	}

	// Phase 3, write B's only fields
	for _, fieldHash := range ctx.FieldsOrder.B {
		field := ctx.StorageB.Fields[fieldHash]

		// Write field header to temporary fields file
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}

		copy(ctx.Buffer[:], pointers.UnsafeSlice(&field.AvgDocumentLength))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
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

			binary.NativeEndian.PutUint32(ctx.Buffer[:4], ctx.DocumentOffset+dl.Index)
			_, err = ctx.DstW.Write(ctx.Buffer[:4])
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

			copy(ctx.Buffer[:], pointers.UnsafeSlice(&token.Idf))
			_, err = ctx.DstW.Write(ctx.Buffer[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token idf: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
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

			// Write the actual token
			_, err = ctx.DstW.Write(pointers.UnsafeSlice(&token.Value))
			if err != nil {
				return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
		}
	}

	// Phase 4, add collision fields
	for _, fieldHash := range ctx.FieldsOrder.Collision {
		fieldA := a.Fields[fieldHash]
		fieldB := b.Fields[fieldHash]

		var totalDocumentLengths = fieldA.TotalDocumentsLength + fieldB.TotalDocumentsLength
		var avgDocumentLength = float32(float64(totalDocumentLengths) / float64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths)))
		var tokensCount = CountTokensBetweenCollisionFields(fieldA, fieldB)

		// Write the field header inmediatly
		_, err = ctx.DstW.Write(pointers.UnsafeSlice(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write collision field field hash: %w: %d", err, fieldHash)
		}

		copy(ctx.Buffer[:], pointers.UnsafeSlice(&avgDocumentLength))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
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
			binary.NativeEndian.PutUint32(ctx.Buffer[:4], ctx.DocumentOffset+dl.Index)
			_, err = ctx.DstW.Write(ctx.Buffer[:4])
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
					err = m.writeCollisionToken(&ctx, fieldA, fieldB, fieldHash, nil, &fieldB.Tokens[bIdx])
					bIdx++
				case bIdx >= bLen:
					err = m.writeCollisionToken(&ctx, fieldA, fieldB, fieldHash, &fieldA.Tokens[aIdx], nil)
					aIdx++
				default:
					switch bytes.Compare(fieldA.Tokens[aIdx].Value.Bytes(), fieldB.Tokens[bIdx].Value.Bytes()) {
					case 0:
						err = m.writeCollisionToken(&ctx, fieldA, fieldB, fieldHash, &fieldA.Tokens[aIdx], &fieldB.Tokens[bIdx])
						aIdx++
						bIdx++
					case -1:
						err = m.writeCollisionToken(&ctx, fieldA, fieldB, fieldHash, &fieldA.Tokens[aIdx], nil)
						aIdx++
					default:
						err = m.writeCollisionToken(&ctx, fieldA, fieldB, fieldHash, nil, &fieldB.Tokens[bIdx])
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

	if mmapMemFile != nil {
		err = unix.Msync(mmapMemFile, unix.MS_SYNC)
		if err != nil {
			return fmt.Errorf("failed to sync changes to disk: %w", err)
		}
	}

	ctx.DstW.Flush()

	ctx.DstW.Reset(ctx.DstFile)

	for _, pending := range ctx.PostingLists {
		switch {
		case pending.IndexA != -1 && pending.IndexB != -1:
			rawA := &a.PostingLists[pending.IndexA]
			rawB := &b.PostingLists[pending.IndexB]

			ctx.ReusableBitmap.Clear()
			rawA.UnsafeBitmap(&ctx.BitmapForPostingListRetrieval)
			ctx.ReusableBitmap.Or(&ctx.BitmapForPostingListRetrieval)

			rawB.UnsafeBitmap(&ctx.BitmapForPostingListRetrieval)
			addOffsetFrom(&ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

			size := ctx.ReusableBitmap.GetSerializedSizeInBytes()
			binary.NativeEndian.PutUint32(ctx.Buffer[:4], uint32(size))
			_, err = ctx.DstW.Write(ctx.Buffer[:4])
			if err != nil {
				return fmt.Errorf("failed to write length of posting list A&B: %w", err)
			}

			_, err = ctx.ReusableBitmap.WriteTo(ctx.DstW)
			if err != nil {
				return fmt.Errorf("failed to write contents of posting list A&B: %w", err)
			}
		case pending.IndexA != -1:
			rawA := &a.PostingLists[pending.IndexA]

			binary.NativeEndian.PutUint32(ctx.Buffer[:4], uint32(len(rawA.Data)))
			_, err = ctx.DstW.Write(ctx.Buffer[:4])
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
			rawB.UnsafeBitmap(&ctx.BitmapForPostingListRetrieval)

			addOffsetFrom(&ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

			size := ctx.ReusableBitmap.GetSerializedSizeInBytes()
			binary.NativeEndian.PutUint32(ctx.Buffer[:4], uint32(size))
			_, err = ctx.DstW.Write(ctx.Buffer[:4])
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
		FieldCount:            ctx.FieldsOrder.Count(),
		TotalPostingLists:     ctx.PostingListCursor,
		TotalTokenFrequencies: ctx.FrequenciesCursor,
	}

	_, err = ctx.DstFile.WriteAt(pointers.UnsafeSlice(&header), 0)
	if err != nil {
		return fmt.Errorf("failed to write header: %w ", err)
	}

	return nil
}
