package storage

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/RogueTeam/textiplex/pointers"
	"golang.org/x/sys/unix"
)

// Handles merges between storages
type Merger struct {
	// Directory for scratch files used during a merge. The output itself is
	// always written next to its final name.
	TempDir string
	// Never replace an existing file at the output name: Merge fails with an
	// error matching os.ErrExist instead.
	NoReplace bool
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

			err = writeFrequencies(ctx, token)
			if err != nil {
				return err
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
			err = writeShiftedFrequencies(ctx, token)
			if err != nil {
				return err
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

					err = writeShiftedFrequencies(ctx, tokenB)
					if err != nil {
						return err
					}

					bIdx++
				case bIdx >= bLen:
					tokenA := &fieldA.Tokens[aIdx]

					err = writeFrequencies(ctx, tokenA)
					if err != nil {
						return err
					}

					aIdx++
				default:
					switch bytes.Compare(fieldA.Tokens[aIdx].Value.Bytes(), fieldB.Tokens[bIdx].Value.Bytes()) {
					case 0:
						tokenA := &fieldA.Tokens[aIdx]
						tokenB := &fieldB.Tokens[bIdx]

						err = writeFrequencies(ctx, tokenA)
						if err != nil {
							return err
						}

						err = writeShiftedFrequencies(ctx, tokenB)
						if err != nil {
							return err
						}

						aIdx++
						bIdx++
					case -1:
						tokenA := &fieldA.Tokens[aIdx]

						err = writeFrequencies(ctx, tokenA)
						if err != nil {
							return err
						}

						aIdx++
					default:
						tokenB := &fieldB.Tokens[bIdx]

						err = writeShiftedFrequencies(ctx, tokenB)
						if err != nil {
							return err
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

// writeFrequencies writes A's frequencies unchanged (A's documents keep their
// indexes), after checking every document index against A's document table.
func writeFrequencies(ctx *MergeContext, token *Token) (err error) {
	freqs, err := ctx.StorageA.frequenciesOf(token)
	if err != nil {
		return fmt.Errorf("storage A: %w", err)
	}
	if len(freqs) == 0 {
		return nil
	}
	aDocuments := uint32(len(ctx.StorageA.DocumentsIds))
	for index := range freqs {
		if freqs[index].DocumentIndex >= aDocuments {
			return fmt.Errorf("storage A: %w", corruptf("token %q references document %d of %d", token.Value.Bytes(), freqs[index].DocumentIndex, aDocuments))
		}
	}
	_, err = ctx.DstW.Write(pointers.UnsafeSliceBytes(freqs))
	if err != nil {
		return fmt.Errorf("failed to write A's token frequencies: %w", err)
	}
	return nil
}

// writeShiftedFrequencies writes B's frequencies for token with every document
// index shifted past A's documents.
func writeShiftedFrequencies(ctx *MergeContext, token *Token) (err error) {
	freqs, err := ctx.StorageB.frequenciesOf(token)
	if err != nil {
		return fmt.Errorf("storage B: %w", err)
	}
	bDocuments := uint32(len(ctx.StorageB.DocumentsIds))
	for index := range freqs {
		freq := &freqs[index]
		if freq.DocumentIndex >= bDocuments {
			return fmt.Errorf("storage B: %w", corruptf("token %q references document %d of %d", token.Value.Bytes(), freq.DocumentIndex, bDocuments))
		}

		binary.NativeEndian.PutUint32(ctx.Buffer[:4], ctx.DocumentOffset+freq.DocumentIndex)
		_, err = ctx.DstW.Write(ctx.Buffer[:4])
		if err != nil {
			return fmt.Errorf("failed to write B's token frequency index: %w", err)
		}

		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&freq.Frequency))
		if err != nil {
			return fmt.Errorf("failed to write B's token frequency: %w", err)
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
	PendingPostingLists                           []PendingPostingList
	Buffer                                        [8]byte
	CachedBitmapChunk                             [OffsetBitmapCachedSize]uint32
	DocumentOffset                                uint32
	ReusableBitmap, BitmapForPostingListRetrieval roaring.Bitmap
	// Iteration order
	FieldsOrder FieldsOrder
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

		ctx.PendingPostingLists = append(ctx.PendingPostingLists, PendingPostingList{
			IndexA: int64(tokenA.PostingListIndex),
			IndexB: int64(tokenB.PostingListIndex),
		})
	case tokenA != nil:
		finalToken = *tokenA
		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		// Write the posting list
		ctx.PendingPostingLists = append(ctx.PendingPostingLists, PendingPostingList{
			IndexA: int64(tokenA.PostingListIndex),
			IndexB: -1,
		})
	case tokenB != nil:
		finalToken = *tokenB
		finalToken.PostingListIndex = ctx.PostingListCursor
		ctx.PostingListCursor++
		finalToken.FrequenciesIndex = ctx.FrequenciesCursor
		ctx.FrequenciesCursor += finalToken.FrequencyCount

		// Write the posting list
		ctx.PendingPostingLists = append(ctx.PendingPostingLists, PendingPostingList{
			IndexA: -1,
			IndexB: int64(tokenB.PostingListIndex),
		})
	}

	_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&finalToken))
	if err != nil {
		return fmt.Errorf("failed to write Collision field token: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
	}
	return nil
}

// Merge merges storages A and B into the file name; B's documents follow A's.
// Document ids should not collide in both storage
// otherwise undefined behavior will ocurr
//
// The output is written under a temporary name next to name and only renamed
// into place once it is complete and synced, so name never holds a partial
// file and a failure leaves any existing file at name untouched. Damaged
// inputs make Merge return an error wrapping ErrCorrupt rather than panic or
// silently drop postings.
func (m *Merger) Merge(name string, a, b *Storage) (err error) {
	if total := uint64(len(a.DocumentsIds)) + uint64(len(b.DocumentsIds)); total > MaxDocuments {
		return fmt.Errorf("%w: merging %d documents", ErrTooManyDocuments, total)
	}
	var ctx = MergeContext{
		DocumentOffset: uint32(len(a.DocumentsIds)),
		StorageA:       a,
		StorageB:       b,
	}

	af, err := createAtomic(name, m.NoReplace)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer af.Abort()
	ctx.DstFile = af.File

	// Reserve the space for the header and the document ids inmediatly
	docIdsSize := (int64(DocumentIdSize) * (int64(len(a.DocumentsIds)) + int64(len(b.DocumentsIds))))
	reserveSize := int64(HeaderSize) + docIdsSize

	err = ctx.DstFile.Truncate(reserveSize)
	if err != nil {
		return fmt.Errorf("failed to reserve header and document ids: %w", err)
	}
	// Seek after the header to write document ids directly
	_, err = ctx.DstFile.Seek(int64(HeaderSize), io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek past the header: %w", err)
	}

	if len(a.DocumentsIds) > 0 {
		_, err := ctx.DstFile.Write(pointers.UnsafeSliceBytes(a.DocumentsIds))
		if err != nil {
			return fmt.Errorf("failed to write storage A's document ids: %w", err)
		}
	}

	if len(b.DocumentsIds) > 0 {
		_, err = ctx.DstFile.Write(pointers.UnsafeSliceBytes(b.DocumentsIds))
		if err != nil {
			return fmt.Errorf("failed to write storage B's document ids: %w", err)
		}
	}

	_, err = ctx.DstFile.Seek(reserveSize, io.SeekStart)
	if err != nil {
		return fmt.Errorf("failed to seek past the document ids: %w", err)
	}

	ctx.DstW = bufio.NewWriterSize(ctx.DstFile, DefaultBufferedWriterSize)

	err = m.preparationPass(&ctx)
	if err != nil {
		return fmt.Errorf("failed to write token frequencies: %w", err)
	}

	var memFile *os.File
	var mmapMemFile []byte
	if ctx.PostingListCount > 0 {
		plSize := PendingPostingListSize * uintptr(ctx.PostingListCount)
		necessarySize := int64(plSize)

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

		ptr := (*PendingPostingList)(unsafe.Pointer(&mmapMemFile[0]))
		ctx.PendingPostingLists = unsafe.Slice(ptr, ctx.PostingListCount)[:0]
	}

	// Phase 2, write A's only fields
	for _, fieldHash := range ctx.FieldsOrder.A {
		field := ctx.StorageA.Fields[fieldHash]

		// Write field header to temporary fields file
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write A's field hash: %w: %d", err, fieldHash)
		}
		copy(ctx.Buffer[:], pointers.UnsafeValueBytes(&field.AvgDocumentLength))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's field avgdl: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&field.TotalDocumentsLength))
		if err != nil {
			return fmt.Errorf("failed to write A's field total document lengths: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(field.Tokens)))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's tokens length: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&field.TotalTokenFrequenciesCount))
		if err != nil {
			return fmt.Errorf("failed to write A's total frequencies count: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(field.DocumentLengths)))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write A's documents lengths: %w: %d", err, fieldHash)
		}
		if len(field.DocumentLengths) > 0 {
			_, err = ctx.DstW.Write(pointers.UnsafeSliceBytes(field.DocumentLengths))
			if err != nil {
				return fmt.Errorf("failed to write storage Field Document length ids: %w", err)
			}
		}

		// Write posting lists
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&token.FrequencyCount))
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
			_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&token.Value))
			if err != nil {
				return fmt.Errorf("failed to write A's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to the posting lists temporary file
			ctx.PendingPostingLists = append(ctx.PendingPostingLists, PendingPostingList{
				IndexA: int64(token.PostingListIndex),
				IndexB: -1,
			})
		}
	}

	// Phase 3, write B's only fields
	for _, fieldHash := range ctx.FieldsOrder.B {
		field := ctx.StorageB.Fields[fieldHash]

		// Write field header to temporary fields file
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}

		copy(ctx.Buffer[:], pointers.UnsafeValueBytes(&field.AvgDocumentLength))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&field.TotalDocumentsLength))
		if err != nil {
			return fmt.Errorf("failed to write B's field total document lengths: %w: %d", err, fieldHash)
		}
		binary.NativeEndian.PutUint64(ctx.Buffer[:], uint64(len(field.Tokens)))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&field.TotalTokenFrequenciesCount))
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

			_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&dl.Length))
			if err != nil {
				return fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, dl.Index)
			}
		}

		// Write posting lists
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&token.FrequencyCount))
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
			ctx.PendingPostingLists = append(ctx.PendingPostingLists, PendingPostingList{
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
			_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&token.Value))
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
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&fieldHash))
		if err != nil {
			return fmt.Errorf("failed to write collision field field hash: %w: %d", err, fieldHash)
		}

		copy(ctx.Buffer[:], pointers.UnsafeValueBytes(&avgDocumentLength))
		_, err = ctx.DstW.Write(ctx.Buffer[:])
		if err != nil {
			return fmt.Errorf("failed to write collision field avgdl: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&totalDocumentLengths))
		if err != nil {
			return fmt.Errorf("failed to write collision field total document length: %w: %d", err, fieldHash)
		}
		_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&tokensCount))
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
			_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(dl))
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

			_, err = ctx.DstW.Write(pointers.UnsafeValueBytes(&dl.Length))
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

	if mmapMemFile != nil {
		err = unix.Msync(mmapMemFile, unix.MS_SYNC)
		if err != nil {
			return fmt.Errorf("failed to sync changes to disk: %w", err)
		}
	}

	err = ctx.DstW.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush fields: %w", err)
	}

	info, err := ctx.DstFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	var writeOffset = info.Size()
	plHeadersSize := uintptr(len(ctx.PendingPostingLists)) * PostingListHeaderSize
	err = ctx.DstFile.Truncate(writeOffset + int64(plHeadersSize))
	if err != nil {
		return fmt.Errorf("failed to reserve posting list headers: %w", err)
	}

	_, err = ctx.DstFile.Seek(0, io.SeekEnd)
	if err != nil {
		return fmt.Errorf("failed to seek to the end of the file: %w", err)
	}

	ctx.DstW.Reset(ctx.DstFile)

	maxPlHeaders := MaxPostingListHeadersBatch(len(ctx.PendingPostingLists))
	var plHeaders = make([]PostingListHeader, 0, maxPlHeaders)
	var plTotalDataSize, plHeadersOffset uint64
	for _, pending := range ctx.PendingPostingLists {
		switch {
		case pending.IndexA != -1 && pending.IndexB != -1:
			ctx.ReusableBitmap.Clear()
			if err := a.PostingLists.Bitmap(pending.IndexA, &ctx.BitmapForPostingListRetrieval); err != nil {
				return fmt.Errorf("storage A: %w", err)
			}
			ctx.ReusableBitmap.Or(&ctx.BitmapForPostingListRetrieval)

			if err := b.PostingLists.Bitmap(pending.IndexB, &ctx.BitmapForPostingListRetrieval); err != nil {
				return fmt.Errorf("storage B: %w", err)
			}
			addOffsetFrom(&ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

			size := ctx.ReusableBitmap.GetSerializedSizeInBytes()
			plHeaders = append(plHeaders, PostingListHeader{
				Offset: plHeadersOffset,
				Length: size,
			})
			plHeadersOffset += size
			if len(plHeaders) >= maxPlHeaders {
				delta, err := ctx.DstFile.WriteAt(pointers.UnsafeSliceBytes(plHeaders), writeOffset)
				if err != nil {
					return fmt.Errorf("failed to write A&B header: %w", err)
				}
				writeOffset += int64(delta)
				plHeaders = plHeaders[:0]
			}

			nn, err := ctx.ReusableBitmap.WriteTo(ctx.DstW)
			plTotalDataSize += uint64(nn)
			if err != nil {
				return fmt.Errorf("failed to write contents of posting list A&B: %w", err)
			}
		case pending.IndexA != -1:
			// Copied verbatim: A's document indexes do not change. Decode it once
			// anyway so a damaged posting list is reported, not propagated.
			rawA, err := a.PostingLists.data(pending.IndexA)
			if err != nil {
				return fmt.Errorf("storage A: %w", err)
			}
			if err = a.PostingLists.Bitmap(pending.IndexA, &ctx.BitmapForPostingListRetrieval); err != nil {
				return fmt.Errorf("storage A: %w", err)
			}

			plHeaders = append(plHeaders, PostingListHeader{
				Offset: plHeadersOffset,
				Length: uint64(len(rawA)),
			})
			plHeadersOffset += uint64(len(rawA))
			if len(plHeaders) >= maxPlHeaders {
				delta, err := ctx.DstFile.WriteAt(pointers.UnsafeSliceBytes(plHeaders), writeOffset)
				if err != nil {
					return fmt.Errorf("failed to write A&B header: %w", err)
				}
				writeOffset += int64(delta)
				plHeaders = plHeaders[:0]
			}

			nn, err := ctx.DstW.Write(rawA)
			plTotalDataSize += uint64(nn)
			if err != nil {
				return fmt.Errorf("failed to write contents of posting list A: %w", err)
			}
		case pending.IndexB != -1:
			ctx.ReusableBitmap.Clear()
			if err := b.PostingLists.Bitmap(pending.IndexB, &ctx.BitmapForPostingListRetrieval); err != nil {
				return fmt.Errorf("storage B: %w", err)
			}

			addOffsetFrom(&ctx, &ctx.ReusableBitmap, &ctx.BitmapForPostingListRetrieval)

			size := ctx.ReusableBitmap.GetSerializedSizeInBytes()
			plHeaders = append(plHeaders, PostingListHeader{
				Offset: plHeadersOffset,
				Length: size,
			})
			plHeadersOffset += size
			if len(plHeaders) >= maxPlHeaders {
				delta, err := ctx.DstFile.WriteAt(pointers.UnsafeSliceBytes(plHeaders), writeOffset)
				if err != nil {
					return fmt.Errorf("failed to write A&B header: %w", err)
				}
				writeOffset += int64(delta)
				plHeaders = plHeaders[:0]
			}

			nn, err := ctx.ReusableBitmap.WriteTo(ctx.DstW)
			plTotalDataSize += uint64(nn)
			if err != nil {
				return fmt.Errorf("failed to write contents of posting list A&B: %w", err)
			}
		}
	}

	if len(plHeaders) > 0 {
		delta, err := ctx.DstFile.WriteAt(pointers.UnsafeSliceBytes(plHeaders), writeOffset)
		if err != nil {
			return fmt.Errorf("failed to write A&B header: %w", err)
		}
		writeOffset += int64(delta)
		plHeaders = plHeaders[:0]
	}

	err = ctx.DstW.Flush()
	if err != nil {
		return fmt.Errorf("failed to flush posting lists: %w", err)
	}

	// File Header
	header := Header{
		Magic:                     MagicNumber,
		Version:                   CurrentVersion,
		TotalDocuments:            uint32(len(a.DocumentsIds)) + uint32(len(b.DocumentsIds)),
		FieldCount:                ctx.FieldsOrder.Count(),
		TotalPostingLists:         ctx.PostingListCursor,
		TotalPostingListsDataSize: plTotalDataSize,
		TotalTokenFrequencies:     ctx.FrequenciesCursor,
	}

	_, err = ctx.DstFile.WriteAt(pointers.UnsafeValueBytes(&header), 0)
	if err != nil {
		return fmt.Errorf("failed to write header: %w ", err)
	}

	if err = appendFooter(ctx.DstFile); err != nil {
		return err
	}
	if err = af.Commit(); err != nil {
		return fmt.Errorf("failed to commit %s: %w", name, err)
	}
	return nil
}
