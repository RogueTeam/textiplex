package storage

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
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

// Merges storages B and B into the specified file
// Document ids should not collide in both storage
// otherwise undefined behavior will ocurr
func (m *Merger) Merge(name string, a, b *Storage) (err error) {
	docOffset := uint32(len(a.DocumentsIds))
	var postingListsCursor, freqsCursor uint64
	// Buffer to be used for binary encoding data
	var buffer [8]byte

	tmpDocIdsFile, err := m.CreateTemp("tmp_docids_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for documents ids: %w", err)
	}
	defer CloseAndRemove(tmpDocIdsFile)

	var errorsCh = make(chan error, 2)
	var wg sync.WaitGroup

	// Phase 1, write document ids to temporary file
	wg.Go(func() {
		if len(a.DocumentsIds) > 0 {
			aDocsSlice := unsafe.Slice((*byte)(unsafe.Pointer(&a.DocumentsIds[0])), DocumentIdSize*uintptr(len(a.DocumentsIds)))
			_, err := tmpDocIdsFile.Write(aDocsSlice)
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write storage A's document ids: %w", err)
				return
			}
		}

		if len(b.DocumentsIds) > 0 {
			bDocsSlice := unsafe.Slice((*byte)(unsafe.Pointer(&b.DocumentsIds[0])), DocumentIdSize*uintptr(len(b.DocumentsIds)))
			_, err = tmpDocIdsFile.Write(bDocsSlice)
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write storage B's document ids: %w", err)
				return
			}
		}
	})

	// Prepare fields, posting lists and token frequencies
	tmpFieldFile, err := m.CreateTemp("tmp_fields_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for fields: %w", err)
	}
	defer CloseAndRemove(tmpFieldFile)

	fieldsW := bufio.NewWriterSize(tmpFieldFile, 4<<20)

	tmpPostingsFile, err := m.CreateTemp("tmp_postinglists_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for posting lists: %w", err)
	}
	defer CloseAndRemove(tmpPostingsFile)

	postingsW := bufio.NewWriterSize(tmpPostingsFile, 4<<20)

	tmpTokenFreqsFile, err := m.CreateTemp("tmp_tokenfreqs_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for token frequencies: %w", err)
	}
	defer CloseAndRemove(tmpTokenFreqsFile)

	tokenFreqsW := bufio.NewWriterSize(tmpTokenFreqsFile, 4<<20)

	var fieldCollisionsCount uint64
	wg.Go(func() {
		var fieldCollisions = make([]uint64, 0, len(a.Fields))

		// Phase 2, write A's only fields
		for fieldHash, field := range a.Fields {
			_, found := b.Fields[fieldHash]
			if found {
				// Do not process collision fields yet
				fieldCollisions = append(fieldCollisions, fieldHash)
				fieldCollisionsCount++
				continue
			}

			// Write field header to temporary fields file
			_, err := fieldsW.Write(pointers.UnsafeSlice(&fieldHash))
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write A's field hash: %w: %d", err, fieldHash)
				return
			}
			_, err = fieldsW.Write(pointers.UnsafeSlice(&field.AvgDocumentLength))
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write A's field avgdl: %w: %d", err, fieldHash)
				return
			}
			binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.Tokens)))
			_, err = fieldsW.Write(buffer[:])
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write A's tokens length: %w: %d", err, fieldHash)
				return
			}
			binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.DocumentLengths)))
			_, err = fieldsW.Write(buffer[:])
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write A's documents lengths: %w: %d", err, fieldHash)
				return
			}

			if len(field.DocumentLengths) > 0 {
				fieldDocLengths := unsafe.Slice((*byte)(unsafe.Pointer(&field.DocumentLengths[0])), DocumentLengthEntrySize*uintptr(len(field.DocumentLengths)))
				_, err = fieldsW.Write(fieldDocLengths)
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write storage Field Document length ids: %w", err)
					return
				}
			}

			// Write posting lists
			for tokenIdx := range field.Tokens {
				token := &field.Tokens[tokenIdx]

				_, err = fieldsW.Write(pointers.UnsafeSlice(&token.FrequencyCount))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write A's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}

				// Add posting list
				_, err = fieldsW.Write(pointers.UnsafeSlice(&postingListsCursor))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write A's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}
				postingListsCursor++

				// Add token frequency
				_, err = fieldsW.Write(pointers.UnsafeSlice(&freqsCursor))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write A's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}
				freqsCursor += token.FrequencyCount

				// Write the actual token
				_, err = fieldsW.Write(pointers.UnsafeSlice(&token.Value))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write A's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}

				// Write directly to the posting lists temporary file
				postingList := &a.PostingLists[token.PostingListIndex]

				binary.NativeEndian.PutUint64(buffer[:], uint64(len(postingList.Data)))
				_, err = postingsW.Write(buffer[:])
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write A's field token posting list size: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}
				_, err = postingsW.Write(postingList.Data)
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write A's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}

				// Write directly to frequencies temporary file
				freqs := a.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]
				if len(freqs) > 0 {
					freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqs[0])), TokenFrequencyEntrySize*uintptr(len(freqs)))
					_, err = tokenFreqsW.Write(freqsBytes)
					if err != nil {
						errorsCh <- fmt.Errorf("failed to write storage frequencies: %w", err)
						return
					}
				}
			}
		}

		var cachedBitmapChunk [OffsetBitmapCachedSize]uint32
		var bitmapForPostingListRetrieval roaring.Bitmap
		var reusableBitmap roaring.Bitmap

		// Phase 3, write B's only fields
		for fieldHash, field := range b.Fields {
			_, found := a.Fields[fieldHash]
			if found {
				continue
			}

			// Write field header to temporary fields file
			_, err := fieldsW.Write(pointers.UnsafeSlice(&fieldHash))
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
				return
			}
			_, err = fieldsW.Write(pointers.UnsafeSlice(&field.AvgDocumentLength))
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
				return
			}
			binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.Tokens)))
			_, err = fieldsW.Write(buffer[:])
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
				return
			}
			binary.NativeEndian.PutUint64(buffer[:], uint64(len(field.DocumentLengths)))
			_, err = fieldsW.Write(buffer[:])
			if err != nil {
				errorsCh <- fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
				return
			}

			for index := range field.DocumentLengths {
				dl := &field.DocumentLengths[index]

				binary.NativeEndian.PutUint32(buffer[:], docOffset+dl.Index)
				_, err = fieldsW.Write(buffer[:])
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write B's document length index: %w: %d:%d", err, fieldHash, dl.Index)
					return
				}

				_, err = fieldsW.Write(pointers.UnsafeSlice(&dl.Length))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, dl.Index)
					return
				}
			}

			// Write posting lists
			for tokenIdx := range field.Tokens {
				token := &field.Tokens[tokenIdx]

				_, err = fieldsW.Write(pointers.UnsafeSlice(&token.FrequencyCount))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write B's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}

				// Add posting list
				_, err = fieldsW.Write(pointers.UnsafeSlice(&postingListsCursor))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write B's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}
				postingListsCursor++

				// Write directly to the posting lists temporary file
				b.PostingLists[token.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)

				reusableBitmap.Clear()

				addOffsetFrom(&reusableBitmap, &bitmapForPostingListRetrieval, &cachedBitmapChunk, docOffset)

				size := reusableBitmap.GetSerializedSizeInBytes()

				_, err = postingsW.Write(pointers.UnsafeSlice(&size))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}
				_, err = reusableBitmap.WriteTo(postingsW)
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}

				// Add token frequency
				_, err = fieldsW.Write(pointers.UnsafeSlice(&freqsCursor))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}
				freqsCursor += token.FrequencyCount

				// Write directly to frequencies temporary file
				freqs := b.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

				for index := range freqs {
					freq := &freqs[index]

					binary.NativeEndian.PutUint32(buffer[:], docOffset+freq.DocumentIndex)
					_, err = tokenFreqsW.Write(buffer[:])
					if err != nil {
						errorsCh <- fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
						return
					}

					_, err = tokenFreqsW.Write(pointers.UnsafeSlice(&freq.Frequency))
					if err != nil {
						errorsCh <- fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
						return
					}
				}

				// Write the actual token
				_, err = fieldsW.Write(pointers.UnsafeSlice(&token.Value))
				if err != nil {
					errorsCh <- fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
					return
				}
			}
		}

		// Helper function that resolves merging both, token A and token B
		var finalTokensCount uint64
		writeToken := func(fieldTokensW io.Writer, fieldHash uint64, tokenA, tokenB *Token) (err error) {
			finalTokensCount++
			var finalToken Token
			switch {
			case tokenA != nil && tokenB != nil: // Equal
				finalToken = *tokenA
				finalToken.FrequencyCount = tokenA.FrequencyCount + tokenB.FrequencyCount
				finalToken.PostingListIndex = postingListsCursor
				postingListsCursor++
				finalToken.FrequenciesIndex = freqsCursor
				freqsCursor += finalToken.FrequencyCount

				reusableBitmap.Clear()

				a.PostingLists[tokenA.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)
				reusableBitmap.Or(&bitmapForPostingListRetrieval)

				b.PostingLists[tokenB.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)

				addOffsetFrom(&reusableBitmap, &bitmapForPostingListRetrieval, &cachedBitmapChunk, docOffset)

				// Write the posting list
				size := reusableBitmap.GetSerializedSizeInBytes()

				_, err := postingsW.Write(pointers.UnsafeSlice(&size))
				if err != nil {
					return fmt.Errorf("failed to write Collision field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
				}

				_, err = reusableBitmap.WriteTo(postingsW)
				if err != nil {
					return fmt.Errorf("failed to write Collision field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
				}

				// Write the frequencies
				freqsA := a.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
				freqsB := b.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

				if len(freqsA) > 0 {
					freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqsA[0])), TokenFrequencyEntrySize*uintptr(len(freqsA)))
					_, err = tokenFreqsW.Write(freqsBytes)
					if err != nil {
						return fmt.Errorf("failed to write A' storage frequencies: %w", err)
					}
				}

				for index := range freqsB {
					freq := &freqsB[index]

					binary.NativeEndian.PutUint32(buffer[:], docOffset+freq.DocumentIndex)
					_, err = tokenFreqsW.Write(buffer[:])
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency: %w: %d", err, fieldHash)
					}

					_, err = tokenFreqsW.Write(pointers.UnsafeSlice(&freq.Frequency))
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency: %w: %d", err, fieldHash)
					}
				}
			case tokenA != nil:
				finalToken = *tokenA
				finalToken.PostingListIndex = postingListsCursor
				postingListsCursor++
				finalToken.FrequenciesIndex = freqsCursor
				freqsCursor += finalToken.FrequencyCount

				// Write the posting list
				postingList := &a.PostingLists[tokenA.PostingListIndex]

				binary.NativeEndian.PutUint64(buffer[:], uint64(len(postingList.Data)))
				_, err = postingsW.Write(buffer[:])
				if err != nil {
					return fmt.Errorf("failed to write A's field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
				}
				_, err := postingsW.Write(postingList.Data)
				if err != nil {
					return fmt.Errorf("failed to write A's field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
				}

				// Write the frequencies
				freqs := a.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
				if len(freqs) > 0 {
					freqsBytes := unsafe.Slice((*byte)(unsafe.Pointer(&freqs[0])), TokenFrequencyEntrySize*uintptr(len(freqs)))
					_, err = tokenFreqsW.Write(freqsBytes)
					if err != nil {
						return fmt.Errorf("failed to write A' storage frequencies: %w", err)
					}
				}
			case tokenB != nil:
				finalToken = *tokenB
				finalToken.PostingListIndex = postingListsCursor
				postingListsCursor++
				finalToken.FrequenciesIndex = freqsCursor
				freqsCursor += finalToken.FrequencyCount

				// Write the posting list
				b.PostingLists[tokenB.PostingListIndex].Bitmap(&bitmapForPostingListRetrieval)

				reusableBitmap.Clear()

				addOffsetFrom(&reusableBitmap, &bitmapForPostingListRetrieval, &cachedBitmapChunk, docOffset)

				size := reusableBitmap.GetSerializedSizeInBytes()

				_, err := postingsW.Write(pointers.UnsafeSlice(&size))
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
				}
				_, err = reusableBitmap.WriteTo(postingsW)
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
				}

				// Write the frequencies
				freqs := b.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

				for index := range freqs {
					freq := &freqs[index]

					binary.NativeEndian.PutUint32(buffer[:], docOffset+freq.DocumentIndex)
					_, err = tokenFreqsW.Write(buffer[:])
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
					}

					_, err = tokenFreqsW.Write(pointers.UnsafeSlice(&freq.Frequency))
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
					}
				}
			}

			_, err = fieldTokensW.Write(pointers.UnsafeSlice(&finalToken))
			if err != nil {
				return fmt.Errorf("failed to write Collision field token: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
			}
			return nil
		}

		tmpFieldTokensFile, err := m.CreateTemp("field-tokens-*.part")
		if err != nil {
			errorsCh <- fmt.Errorf("failed to create temporary field tokens file: %w", err)
			return
		}
		defer func() {
			tmpFieldTokensFile.Close()
			os.Remove(tmpFieldTokensFile.Name())
		}()

		fieldTokensW := bufio.NewWriterSize(tmpFieldTokensFile, 2<<20)

		tmpFieldTokenDocLengths, err := m.CreateTemp("field-tokens-doc-lengths-*")
		if err != nil {
			errorsCh <- fmt.Errorf("failed to create temporary field tokens doc lengths file: %w", err)
			return
		}
		defer func() {
			tmpFieldTokenDocLengths.Close()
			os.Remove(tmpFieldTokenDocLengths.Name())
		}()

		fieldTokenDocLengthsW := bufio.NewWriterSize(tmpFieldTokenDocLengths, 2<<20)

		// Phase 4, add collision fields
		for _, fieldHash := range fieldCollisions {
			tmpFieldTokensFile.Seek(0, 0)
			err = tmpFieldTokensFile.Truncate(0)
			if err != nil {
				errorsCh <- fmt.Errorf("failed to retruncate field tokens file: %w", err)
				return
			}

			fieldTokensW.Reset(tmpFieldTokensFile)

			finalTokensCount = 0
			err := func() (err error) {
				fieldA := a.Fields[fieldHash]
				fieldB := b.Fields[fieldHash]

				aLen, bLen := len(fieldA.Tokens), len(fieldB.Tokens)
				for aIdx, bIdx := 0, 0; aIdx < aLen || bIdx < bLen; {
					switch {
					case aIdx >= aLen:
						err = writeToken(fieldTokensW, fieldHash, nil, &fieldB.Tokens[bIdx])
						bIdx++
					case bIdx >= bLen:
						err = writeToken(fieldTokensW, fieldHash, &fieldA.Tokens[aIdx], nil)
						aIdx++
					default:
						switch bytes.Compare(fieldA.Tokens[aIdx].Value.Bytes(), fieldB.Tokens[bIdx].Value.Bytes()) {
						case 0:
							err = writeToken(fieldTokensW, fieldHash, &fieldA.Tokens[aIdx], &fieldB.Tokens[bIdx])
							aIdx++
							bIdx++
						case -1:
							err = writeToken(fieldTokensW, fieldHash, &fieldA.Tokens[aIdx], nil)
							aIdx++
						default:
							err = writeToken(fieldTokensW, fieldHash, nil, &fieldB.Tokens[bIdx])
							bIdx++
						}
					}
					if err != nil {
						return fmt.Errorf("failed to write collision token: %w: %d", err, fieldHash)
					}
				}

				err = fieldTokensW.Flush()
				if err != nil {
					return fmt.Errorf("failed to flush field tokens to underlying file: %w", err)
				}
				_, err = tmpFieldTokensFile.Seek(0, 0)
				if err != nil {
					return fmt.Errorf("failed to seek field tokens file to beginning: %w", err)
				}

				// Prepare documents lengths
				var totalDocumentLengths uint64

				tmpFieldTokenDocLengths.Seek(0, 0)
				err = tmpFieldTokenDocLengths.Truncate(0)
				if err != nil {
					return fmt.Errorf("failed to retruncate field tokens doc lengths file: %w", err)
				}

				fieldTokenDocLengthsW.Reset(tmpFieldTokenDocLengths)

				for index := range fieldA.DocumentLengths {
					dl := &fieldA.DocumentLengths[index]

					totalDocumentLengths += dl.Length

					_, err = fieldTokenDocLengthsW.Write(pointers.UnsafeSlice(dl))
					if err != nil {
						return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
					}
				}

				for index := range fieldB.DocumentLengths {
					dl := &fieldB.DocumentLengths[index]

					totalDocumentLengths += dl.Length

					binary.NativeEndian.PutUint32(buffer[:], docOffset+dl.Index)
					_, err = fieldTokenDocLengthsW.Write(buffer[:])
					if err != nil {
						return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
					}

					_, err = fieldTokenDocLengthsW.Write(pointers.UnsafeSlice(&dl.Length))
					if err != nil {
						return fmt.Errorf("failed to write Collision document length: %w: %d:%d", err, fieldHash, dl.Index)
					}
				}

				err = fieldTokenDocLengthsW.Flush()
				if err != nil {
					return fmt.Errorf("failed to flush field tokens doc lengths: %w", err)
				}

				_, err = tmpFieldTokenDocLengths.Seek(0, 0)
				if err != nil {
					return fmt.Errorf("failed to seek to the beginning field tokens doc lengths file: %w", err)
				}

				var avgDocumentLength = float64(totalDocumentLengths) / float64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths))

				// Write the field
				// Write field header to temporary fields file
				_, err = fieldsW.Write(pointers.UnsafeSlice(&fieldHash))
				if err != nil {
					return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
				}
				_, err = fieldsW.Write(pointers.UnsafeSlice(&avgDocumentLength))
				if err != nil {
					return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
				}
				_, err = fieldsW.Write(pointers.UnsafeSlice(&finalTokensCount))
				if err != nil {
					return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
				}
				binary.NativeEndian.PutUint64(buffer[:], uint64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths)))
				_, err = fieldsW.Write(buffer[:])
				if err != nil {
					return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
				}

				err = fieldsW.Flush()
				if err != nil {
					return fmt.Errorf("failed to flush remaining field data: %w", err)
				}

				// Write documents lengths
				_, err = tmpFieldFile.ReadFrom(tmpFieldTokenDocLengths)
				if err != nil {
					return fmt.Errorf("failed to merge field tokens doc lengths into field writer: %w", err)
				}

				_, err = tmpFieldFile.ReadFrom(tmpFieldTokensFile)
				if err != nil {
					return fmt.Errorf("failed to merge field tokens into field writer: %w", err)
				}

				return nil
			}()
			if err != nil {
				errorsCh <- fmt.Errorf("failed to handle field hash: %w", err)
				return
			}
		}
	})

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
	file, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer file.Close()

	// File Header
	binary.NativeEndian.PutUint64(buffer[:], MagicNumber)
	_, err = file.Write(buffer[:])
	if err != nil {
		return fmt.Errorf("failed to write header magic number: %w ", err)
	}
	binary.NativeEndian.PutUint16(buffer[:], VersionV1)
	_, err = file.Write(buffer[:])
	if err != nil {
		return fmt.Errorf("failed to write header version: %w ", err)
	}
	binary.NativeEndian.PutUint32(buffer[:], uint32(len(a.DocumentsIds))+uint32(len(b.DocumentsIds)))
	_, err = file.Write(buffer[:])
	if err != nil {
		return fmt.Errorf("failed to write header docs ids count: %w ", err)
	}
	binary.NativeEndian.PutUint64(buffer[:], uint64(len(a.Fields))+uint64(len(b.Fields))-fieldCollisionsCount)
	_, err = file.Write(buffer[:])
	if err != nil {
		return fmt.Errorf("failed to write header fields count: %w ", err)
	}
	binary.NativeEndian.PutUint64(buffer[:], postingListsCursor)
	_, err = file.Write(buffer[:])
	if err != nil {
		return fmt.Errorf("failed to write header posting lists count: %w ", err)
	}
	binary.NativeEndian.PutUint64(buffer[:], freqsCursor)
	_, err = file.Write(buffer[:])
	if err != nil {
		return fmt.Errorf("failed to write header freqs count: %w ", err)
	}

	tmpDocIdsFile.Seek(0, 0)

	fieldsW.Flush()
	tmpFieldFile.Seek(0, 0)

	postingsW.Flush()
	tmpPostingsFile.Seek(0, 0)

	tokenFreqsW.Flush()
	tmpTokenFreqsFile.Seek(0, 0)

	// Hopefully all these calls will use send file or splice internally :)
	_, err = file.ReadFrom(tmpDocIdsFile)
	if err != nil {
		return fmt.Errorf("failed to append doc ids: %w", err)
	}
	_, err = file.ReadFrom(tmpFieldFile)
	if err != nil {
		return fmt.Errorf("failed to append fields: %w", err)
	}
	_, err = file.ReadFrom(tmpPostingsFile)
	if err != nil {
		return fmt.Errorf("failed to append posting lists: %w", err)
	}
	_, err = file.ReadFrom(tmpTokenFreqsFile)
	if err != nil {
		return fmt.Errorf("failed to append token freqs: %w", err)
	}

	return nil
}
