package storage

import (
	"bufio"
	"bytes"
	"cmp"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"github.com/RoaringBitmap/roaring"
	"github.com/RogueTeam/textiplex/pointers"
	"github.com/tidwall/btree"
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

type PendingWrite struct {
	Idx      uint64
	Filename string
}

func (w *PendingWrite) Release() {
	os.Remove(w.Filename)
}

func (m *Merger) writeCollisionToken(
	plCursor, freqsCursor *uint64,
	fieldHash uint64,
	buffer *[8]byte,
	cachedBitmapChunk *[OffsetBitmapCachedSize]uint32,
	docOffset uint32,
	reusableBitmap, bitmapForPostingListRetrieval *roaring.Bitmap,
	a, b *Storage,
	tokensW, plW, tokFreqsW *bufio.Writer,
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

	_, err = tokensW.Write(pointers.UnsafeSlice(&finalToken))
	if err != nil {
		return fmt.Errorf("failed to write Collision field token: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
	}
	return nil
}

// Merges storages B and B into the specified file
// Document ids should not collide in both storage
// otherwise undefined behavior will ocurr
func (m *Merger) Merge(name string, a, b *Storage) (err error) {
	docOffset := uint32(len(a.DocumentsIds))

	var errorsCh = make(chan error, 4)
	var wg sync.WaitGroup

	var (
		pendingFields = btree.NewBTreeGOptions(
			func(a, b *PendingWrite) bool {
				return cmp.Less(a.Idx, b.Idx)
			},
			btree.Options{
				NoLocks: false,
			})
		pendingPls = btree.NewBTreeGOptions(
			func(a, b *PendingWrite) bool {
				return cmp.Less(a.Idx, b.Idx)
			},
			btree.Options{
				NoLocks: false,
			})
		pendingTokenFreqs = btree.NewBTreeGOptions(
			func(a, b *PendingWrite) bool {
				return cmp.Less(a.Idx, b.Idx)
			},
			btree.Options{
				NoLocks: false,
			})
	)
	defer func() {
		func() {
			it := pendingFields.Iter()
			defer it.Release()

			for valid := it.First(); valid; valid = it.Next() {
				it.Item().Release()
			}
		}()
		func() {
			it := pendingPls.Iter()
			defer it.Release()

			for valid := it.First(); valid; valid = it.Next() {
				it.Item().Release()
			}
		}()
		func() {
			it := pendingTokenFreqs.Iter()
			defer it.Release()

			for valid := it.First(); valid; valid = it.Next() {
				it.Item().Release()
			}
		}()
	}()
	var fieldCollisionsCount uint64
	var fieldCollisions = make([]uint64, 0, len(a.Fields))
	var fieldIdx, postingListsCursor, freqsCursor uint64

	// Phase 2, write A's only fields
	for fieldHash, field := range a.Fields {
		_, found := b.Fields[fieldHash]
		if found {
			// Do not process collision fields yet
			fieldCollisions = append(fieldCollisions, fieldHash)
			fieldCollisionsCount++
			continue
		}

		currFieldIdx := fieldIdx
		fieldIdx++
		currPlCursor := postingListsCursor
		postingListsCursor += uint64(len(field.Tokens))
		currFreqsCursor := freqsCursor
		freqsCursor += field.TotalTokenFrequenciesCount

		wg.Go(func() {
			err := func() (err error) {
				var buffer [8]byte

				fieldFile, err := m.CreateTemp(fmt.Sprintf("field-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field file: %w", err)
				}
				defer func() {
					fieldFile.Close()
					if err != nil {
						os.Remove(fieldFile.Name())
					}
				}()

				fieldW := bufio.NewWriterSize(fieldFile, DefaultBufferedWriterSize)

				plFile, err := m.CreateTemp(fmt.Sprintf("field-posting-list-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field's posting list file: %w", err)
				}
				defer func() {
					plFile.Close()
					if err != nil {
						os.Remove(plFile.Name())
					}
				}()

				plW := bufio.NewWriterSize(plFile, DefaultBufferedWriterSize)

				tokFreqsFile, err := m.CreateTemp(fmt.Sprintf("field-token-freqs-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field's token frequencies file: %w", err)
				}
				defer func() {
					tokFreqsFile.Close()
					if err != nil {
						os.Remove(tokFreqsFile.Name())
					}
				}()

				tokFreqsW := bufio.NewWriterSize(tokFreqsFile, DefaultBufferedWriterSize)

				// Write field header to temporary fields file
				_, err = fieldW.Write(pointers.UnsafeSlice(&fieldHash))
				if err != nil {
					return fmt.Errorf("failed to write A's field hash: %w: %d", err, fieldHash)
				}
				_, err = fieldW.Write(pointers.UnsafeSlice(&field.AvgDocumentLength))
				if err != nil {
					return fmt.Errorf("failed to write A's field avgdl: %w: %d", err, fieldHash)
				}
				binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.Tokens)))
				_, err = fieldW.Write(buffer[:])
				if err != nil {
					return fmt.Errorf("failed to write A's tokens length: %w: %d", err, fieldHash)
				}
				_, err = fieldW.Write(pointers.UnsafeSlice(&field.TotalTokenFrequenciesCount))
				if err != nil {
					return fmt.Errorf("failed to write A's total frequencies count: %w: %d", err, fieldHash)
				}
				binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.DocumentLengths)))
				_, err = fieldW.Write(buffer[:])
				if err != nil {
					return fmt.Errorf("failed to write A's documents lengths: %w: %d", err, fieldHash)
				}
				if len(field.DocumentLengths) > 0 {
					fieldDocLengths := unsafe.Slice((*byte)(unsafe.Pointer(&field.DocumentLengths[0])), DocumentLengthEntrySize*uintptr(len(field.DocumentLengths)))
					_, err = fieldW.Write(fieldDocLengths)
					if err != nil {
						return fmt.Errorf("failed to write storage Field Document length ids: %w", err)
					}
				}

				// Write posting lists
				for tokenIdx := range field.Tokens {
					token := &field.Tokens[tokenIdx]

					_, err = fieldW.Write(pointers.UnsafeSlice(&token.FrequencyCount))
					if err != nil {
						return fmt.Errorf("failed to write A's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					}

					// Add posting list
					binary.NativeEndian.PutUint64(buffer[:], currPlCursor)
					currPlCursor++
					_, err = fieldW.Write(buffer[:])
					if err != nil {
						return fmt.Errorf("failed to write A's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					}

					// Add token frequency
					binary.NativeEndian.PutUint64(buffer[:], currFreqsCursor)
					currFreqsCursor += token.FrequencyCount
					_, err = fieldW.Write(buffer[:])
					if err != nil {
						return fmt.Errorf("failed to write A's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					}

					// Write the actual token
					_, err = fieldW.Write(pointers.UnsafeSlice(&token.Value))
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

				fieldW.Flush()
				plW.Flush()
				tokFreqsW.Flush()

				pendingFields.Set(&PendingWrite{Idx: currFieldIdx, Filename: fieldFile.Name()})
				pendingPls.Set(&PendingWrite{Idx: currFieldIdx, Filename: plFile.Name()})
				pendingTokenFreqs.Set(&PendingWrite{Idx: currFieldIdx, Filename: tokFreqsFile.Name()})
				return nil
			}()

			if err != nil {
				errorsCh <- fmt.Errorf("failed to process A's field: %d: %w", fieldHash, err)
				return
			}
		})
	}

	// Phase 3, write B's only fields
	for fieldHash, field := range b.Fields {
		_, found := a.Fields[fieldHash]
		if found {
			continue
		}

		currFieldIdx := fieldIdx
		fieldIdx++
		currPlCursor := postingListsCursor
		postingListsCursor += uint64(len(field.Tokens))
		currFreqsCursor := freqsCursor
		freqsCursor += field.TotalTokenFrequenciesCount

		wg.Go(func() {
			err := func() (err error) {
				var buffer [8]byte

				var cachedBitmapChunk [OffsetBitmapCachedSize]uint32
				var bitmapForPostingListRetrieval roaring.Bitmap
				var reusableBitmap roaring.Bitmap

				fieldFile, err := m.CreateTemp(fmt.Sprintf("field-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field file: %w", err)
				}
				defer func() {
					fieldFile.Close()
					if err != nil {
						os.Remove(fieldFile.Name())
					}
				}()

				fieldW := bufio.NewWriterSize(fieldFile, DefaultBufferedWriterSize)

				plFile, err := m.CreateTemp(fmt.Sprintf("field-posting-list-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field's posting list file: %w", err)
				}
				defer func() {
					plFile.Close()
					if err != nil {
						os.Remove(plFile.Name())
					}
				}()

				plW := bufio.NewWriterSize(plFile, DefaultBufferedWriterSize)

				tokFreqsFile, err := m.CreateTemp(fmt.Sprintf("field-token-freqs-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field's token frequencies file: %w", err)
				}
				defer func() {
					tokFreqsFile.Close()
					if err != nil {
						os.Remove(tokFreqsFile.Name())
					}
				}()

				tokFreqsW := bufio.NewWriterSize(tokFreqsFile, DefaultBufferedWriterSize)

				// Write field header to temporary fields file
				_, err = fieldW.Write(pointers.UnsafeSlice(&fieldHash))
				if err != nil {
					return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
				}
				_, err = fieldW.Write(pointers.UnsafeSlice(&field.AvgDocumentLength))
				if err != nil {
					return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
				}
				binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.Tokens)))
				_, err = fieldW.Write(buffer[:])
				if err != nil {
					return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
				}
				_, err = fieldW.Write(pointers.UnsafeSlice(&field.TotalTokenFrequenciesCount))
				if err != nil {
					return fmt.Errorf("failed to write B's field total frequencies count: %w: %d", err, fieldHash)
				}
				binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.DocumentLengths)))
				_, err = fieldW.Write(buffer[:])
				if err != nil {
					return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
				}

				for index := range field.DocumentLengths {
					dl := &field.DocumentLengths[index]

					binary.NativeEndian.PutUint32(buffer[:], docOffset+dl.Index)
					_, err = fieldW.Write(buffer[:])
					if err != nil {
						return fmt.Errorf("failed to write B's document length index: %w: %d:%d", err, fieldHash, dl.Index)
					}

					_, err = fieldW.Write(pointers.UnsafeSlice(&dl.Length))
					if err != nil {
						return fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, dl.Index)
					}
				}

				// Write posting lists
				for tokenIdx := range field.Tokens {
					token := &field.Tokens[tokenIdx]

					_, err = fieldW.Write(pointers.UnsafeSlice(&token.FrequencyCount))
					if err != nil {
						return fmt.Errorf("failed to write B's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					}

					// Add posting list
					binary.NativeEndian.PutUint64(buffer[:], currPlCursor)
					currPlCursor++
					_, err = fieldW.Write(buffer[:])
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
					binary.NativeEndian.PutUint64(buffer[:], currFreqsCursor)
					currFreqsCursor += token.FrequencyCount
					_, err = fieldW.Write(buffer[:])
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
					_, err = fieldW.Write(pointers.UnsafeSlice(&token.Value))
					if err != nil {
						return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					}
				}

				fieldW.Flush()
				plW.Flush()
				tokFreqsW.Flush()

				pendingFields.Set(&PendingWrite{Idx: currFieldIdx, Filename: fieldFile.Name()})
				pendingPls.Set(&PendingWrite{Idx: currFieldIdx, Filename: plFile.Name()})
				pendingTokenFreqs.Set(&PendingWrite{Idx: currFieldIdx, Filename: tokFreqsFile.Name()})
				return nil
			}()
			if err != nil {
				errorsCh <- fmt.Errorf("failed to process B's field: %d: %w", fieldHash, err)
				return
			}
		})
	}

	// Phase 4, add collision fields
	for _, fieldHash := range fieldCollisions {
		fieldA := a.Fields[fieldHash]
		fieldB := b.Fields[fieldHash]

		currFieldIdx := fieldIdx
		fieldIdx++
		currPlCursor := postingListsCursor
		{
			aLen, bLen := len(fieldA.Tokens), len(fieldB.Tokens)
			for aIdx, bIdx := 0, 0; aIdx < aLen || bIdx < bLen; {
				postingListsCursor++
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
		}
		currFreqsCursor := freqsCursor
		freqsCursor += fieldA.TotalTokenFrequenciesCount + fieldB.TotalTokenFrequenciesCount

		wg.Go(func() {
			var buffer [8]byte

			err := func() (err error) {
				var finalTokensCount uint64
				var cachedBitmapChunk [OffsetBitmapCachedSize]uint32
				var bitmapForPostingListRetrieval roaring.Bitmap
				var reusableBitmap roaring.Bitmap

				fieldFile, err := m.CreateTemp(fmt.Sprintf("field-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field file: %w", err)
				}
				defer func() {
					fieldFile.Close()
					if err != nil {
						os.Remove(fieldFile.Name())
					}
				}()

				fieldW := bufio.NewWriterSize(fieldFile, DefaultBufferedWriterSize)

				plFile, err := m.CreateTemp(fmt.Sprintf("field-posting-list-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field's posting list file: %w", err)
				}
				defer func() {
					plFile.Close()
					if err != nil {
						os.Remove(plFile.Name())
					}
				}()

				plW := bufio.NewWriterSize(plFile, DefaultBufferedWriterSize)

				tokFreqsFile, err := m.CreateTemp(fmt.Sprintf("field-token-freqs-%d-*", fieldHash))
				if err != nil {
					return fmt.Errorf("failed to prepare field's token frequencies file: %w", err)
				}
				defer func() {
					tokFreqsFile.Close()
					if err != nil {
						os.Remove(tokFreqsFile.Name())
					}
				}()

				tokFreqsW := bufio.NewWriterSize(tokFreqsFile, DefaultBufferedWriterSize)

				//

				tmpTokensFile, err := m.CreateTemp("field-tokens-*.part")
				if err != nil {
					return fmt.Errorf("failed to create temporary field tokens file: %w", err)
				}
				defer CloseAndRemove(tmpTokensFile)

				tokensW := bufio.NewWriterSize(tmpTokensFile, DefaultBufferedWriterSize)

				//

				err = func() (err error) {
					aLen, bLen := len(fieldA.Tokens), len(fieldB.Tokens)
					for aIdx, bIdx := 0, 0; aIdx < aLen || bIdx < bLen; {
						finalTokensCount++
						switch {
						case aIdx >= aLen:
							err = m.writeCollisionToken(
								&currPlCursor, &currFreqsCursor,
								fieldHash, &buffer, &cachedBitmapChunk, docOffset,
								&reusableBitmap, &bitmapForPostingListRetrieval,
								a, b,
								tokensW, plW, tokFreqsW,
								nil, &fieldB.Tokens[bIdx],
							)
							bIdx++
						case bIdx >= bLen:
							err = m.writeCollisionToken(
								&currPlCursor, &currFreqsCursor,
								fieldHash, &buffer, &cachedBitmapChunk, docOffset,
								&reusableBitmap, &bitmapForPostingListRetrieval,
								a, b,
								tokensW, plW, tokFreqsW,
								&fieldA.Tokens[aIdx], nil,
							)
							aIdx++
						default:
							switch bytes.Compare(fieldA.Tokens[aIdx].Value.Bytes(), fieldB.Tokens[bIdx].Value.Bytes()) {
							case 0:
								err = m.writeCollisionToken(
									&currPlCursor, &currFreqsCursor,
									fieldHash, &buffer, &cachedBitmapChunk, docOffset,
									&reusableBitmap, &bitmapForPostingListRetrieval,
									a, b,
									tokensW, plW, tokFreqsW,
									&fieldA.Tokens[aIdx], &fieldB.Tokens[bIdx],
								)
								aIdx++
								bIdx++
							case -1:
								err = m.writeCollisionToken(
									&currPlCursor, &currFreqsCursor,
									fieldHash, &buffer, &cachedBitmapChunk, docOffset,
									&reusableBitmap, &bitmapForPostingListRetrieval,
									a, b,
									tokensW, plW, tokFreqsW,
									&fieldA.Tokens[aIdx], nil,
								)
								aIdx++
							default:
								err = m.writeCollisionToken(
									&currPlCursor, &currFreqsCursor,
									fieldHash, &buffer, &cachedBitmapChunk, docOffset,
									&reusableBitmap, &bitmapForPostingListRetrieval,
									a, b,
									tokensW, plW, tokFreqsW,
									nil, &fieldB.Tokens[bIdx],
								)
								bIdx++
							}
						}
						if err != nil {
							return fmt.Errorf("failed to write collision token: %w: %d", err, fieldHash)
						}
					}

					plW.Flush()
					tokFreqsW.Flush()

					err = tokensW.Flush()
					if err != nil {
						return fmt.Errorf("failed to flush field tokens to underlying file: %w", err)
					}
					_, err = tmpTokensFile.Seek(0, 0)
					if err != nil {
						return fmt.Errorf("failed to seek field tokens file to beginning: %w", err)
					}

					// Prepare documents lengths
					var totalDocumentLengths uint64

					tmpDocLengthsFile, err := m.CreateTemp("field-tokens-doc-lengths-*.part")
					if err != nil {
						return fmt.Errorf("failed to create temporary field tokens doc lengths file: %w", err)
					}
					defer CloseAndRemove(tmpDocLengthsFile)

					docLengthsW := bufio.NewWriterSize(tmpDocLengthsFile, DefaultBufferedWriterSize)

					for index := range fieldA.DocumentLengths {
						dl := &fieldA.DocumentLengths[index]

						totalDocumentLengths += dl.Length

						_, err = docLengthsW.Write(pointers.UnsafeSlice(dl))
						if err != nil {
							return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
						}
					}

					for index := range fieldB.DocumentLengths {
						dl := &fieldB.DocumentLengths[index]

						totalDocumentLengths += dl.Length

						binary.NativeEndian.PutUint32(buffer[:], docOffset+dl.Index)
						_, err = docLengthsW.Write(buffer[:])
						if err != nil {
							return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
						}

						_, err = docLengthsW.Write(pointers.UnsafeSlice(&dl.Length))
						if err != nil {
							return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
						}
					}

					err = docLengthsW.Flush()
					if err != nil {
						return fmt.Errorf("failed to flush field tokens doc lengths: %w", err)
					}
					_, err = tmpDocLengthsFile.Seek(0, 0)
					if err != nil {
						return fmt.Errorf("failed to seek to the beginning field tokens doc lengths file: %w", err)
					}

					var avgDocumentLength = float64(totalDocumentLengths) / float64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths))

					// Write the field
					// Write field header to temporary fields file
					_, err = fieldW.Write(pointers.UnsafeSlice(&fieldHash))
					if err != nil {
						return fmt.Errorf("failed to write collision field field hash: %w: %d", err, fieldHash)
					}
					_, err = fieldW.Write(pointers.UnsafeSlice(&avgDocumentLength))
					if err != nil {
						return fmt.Errorf("failed to write collision field avgdl: %w: %d", err, fieldHash)
					}
					_, err = fieldW.Write(pointers.UnsafeSlice(&finalTokensCount))
					if err != nil {
						return fmt.Errorf("failed to write collision field tokens length: %w: %d", err, fieldHash)
					}
					binary.NativeEndian.PutUint64(buffer[:], fieldA.TotalTokenFrequenciesCount+fieldB.TotalTokenFrequenciesCount)
					_, err = fieldW.Write(buffer[:])
					if err != nil {
						return fmt.Errorf("failed to write collision field total tokens freqs count: %w: %d", err, fieldHash)
					}
					binary.NativeEndian.PutUint64(buffer[:], uint64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths)))
					_, err = fieldW.Write(buffer[:])
					if err != nil {
						return fmt.Errorf("failed to write collision field documents lengths: %w: %d", err, fieldHash)
					}

					err = fieldW.Flush()
					if err != nil {
						return fmt.Errorf("failed to flush remaining field data: %w", err)
					}

					// Write documents lengths
					_, err = fieldFile.ReadFrom(tmpDocLengthsFile)
					if err != nil {
						return fmt.Errorf("failed to merge field tokens doc lengths into field writer: %w", err)
					}

					_, err = fieldFile.ReadFrom(tmpTokensFile)
					if err != nil {
						return fmt.Errorf("failed to merge field tokens into field writer: %w", err)
					}

					return nil
				}()
				if err != nil {
					return fmt.Errorf("failed to handle collision field: %d: %w", fieldHash, err)
				}

				//

				pendingFields.Set(&PendingWrite{Idx: currFieldIdx, Filename: fieldFile.Name()})
				pendingPls.Set(&PendingWrite{Idx: currFieldIdx, Filename: plFile.Name()})
				pendingTokenFreqs.Set(&PendingWrite{Idx: currFieldIdx, Filename: tokFreqsFile.Name()})
				return nil
			}()

			if err != nil {
				errorsCh <- fmt.Errorf("failed to process collision field: %d: %w", fieldHash, err)
				return
			}
		})
	}

	go func() { wg.Wait(); close(errorsCh) }()
	var allErrors []error
	for err := range errorsCh {
		allErrors = append(allErrors, err)
	}

	switch len(allErrors) {
	case 0:
	case 1:
		return fmt.Errorf("error during merge: %w", allErrors[0])
	default:
		return fmt.Errorf("multiple errors during merge: %w", errors.Join(allErrors...))
	}

	// Phase 5, Assembly everything
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

	// File Header
	header := Header{
		Magic:                 MagicNumber,
		Version:               VersionV1,
		TotalDocuments:        uint32(len(a.DocumentsIds)) + uint32(len(b.DocumentsIds)),
		FieldCount:            (uint64(len(a.Fields)) + uint64(len(b.Fields))) - fieldCollisionsCount,
		TotalPostingLists:     postingListsCursor,
		TotalTokenFrequencies: freqsCursor,
	}
	_, err = dstFile.Write(pointers.UnsafeSlice(&header))
	if err != nil {
		return fmt.Errorf("failed to write header: %w ", err)
	}

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

	// Pending fields
	err = func() (err error) {
		it := pendingFields.Iter()
		defer it.Release()

		for valid := it.First(); valid; valid = it.Next() {
			write := it.Item()

			err = func() (err error) {
				fieldFile, err := os.Open(write.Filename)
				if err != nil {
					return fmt.Errorf("failed to open field file: %w", err)
				}
				defer CloseAndRemove(fieldFile)

				_, err = dstFile.ReadFrom(fieldFile)
				if err != nil {
					return fmt.Errorf("failed to read from field file: %w", err)
				}

				return nil
			}()
			if err != nil {
				return fmt.Errorf("failed to to process field file: %s: %w", write.Filename, err)
			}
		}

		return nil
	}()
	if err != nil {
		return fmt.Errorf("failed to write field files: %w", err)
	}

	// Pending Posting lists
	err = func() (err error) {
		it := pendingPls.Iter()
		defer it.Release()

		for valid := it.First(); valid; valid = it.Next() {
			write := it.Item()

			err = func() (err error) {
				plFile, err := os.Open(write.Filename)
				if err != nil {
					return fmt.Errorf("failed to open posting list file: %w", err)
				}
				defer CloseAndRemove(plFile)

				_, err = dstFile.ReadFrom(plFile)
				if err != nil {
					return fmt.Errorf("failed to read from posting list file: %w", err)
				}

				return nil
			}()
			if err != nil {
				return fmt.Errorf("failed to to process posting list file: %s: %w", write.Filename, err)
			}
		}

		return nil
	}()
	if err != nil {
		return fmt.Errorf("failed to write posting lists files: %w", err)
	}

	// Pending Posting lists
	err = func() (err error) {
		it := pendingTokenFreqs.Iter()
		defer it.Release()

		for valid := it.First(); valid; valid = it.Next() {
			write := it.Item()

			err = func() (err error) {
				tokFreqsFile, err := os.Open(write.Filename)
				if err != nil {
					return fmt.Errorf("failed to open token frequencies file: %w", err)
				}
				defer CloseAndRemove(tokFreqsFile)

				_, err = dstFile.ReadFrom(tokFreqsFile)
				if err != nil {
					return fmt.Errorf("failed to read from token frequencies file: %w", err)
				}
				return nil
			}()
			if err != nil {
				return fmt.Errorf("failed to to process token frequencies file: %s: %w", write.Filename, err)
			}
		}

		return nil
	}()
	if err != nil {
		return fmt.Errorf("failed to write token frequencies files: %w", err)
	}

	return nil
}
