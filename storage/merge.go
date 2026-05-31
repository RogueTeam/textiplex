package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"unsafe"

	"github.com/RoaringBitmap/roaring/roaring64"
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

// Merges storages B and B into the specified file
// Document ids should not collide in both storage
// otherwise undefined behavior will ocurr
func (m *Merger) Merge(name string, a, b *Storage) (err error) {
	docOffset := uint64(len(a.DocumentsIds))
	var postingListsCursor, freqsCursor uint64
	// Buffer to be used for binary encoding data
	var buffer = make([]byte, 0, 8)

	tmpDocIds, err := m.CreateTemp("tmp_docids_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for documents ids: %w", err)
	}
	defer CloseAndRemove(tmpDocIds)

	// Phase 1, write document ids to temporary file
	for _, docId := range a.DocumentsIds {
		data := binary.NativeEndian.AppendUint16(buffer, uint16(len(docId)))
		_, err = tmpDocIds.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's document id length: %w: %s", err, docId)
		}

		_, err = tmpDocIds.Write(docId)
		if err != nil {
			return fmt.Errorf("failed to write B's document id: %w: %s", err, docId)
		}
	}

	for _, docId := range b.DocumentsIds {
		data := binary.NativeEndian.AppendUint16(buffer, uint16(len(docId)))
		_, err = tmpDocIds.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's document id length: %w: %s", err, docId)
		}

		_, err = tmpDocIds.Write(docId)
		if err != nil {
			return fmt.Errorf("failed to write B's document id: %w: %s", err, docId)
		}
	}

	// Prepare fields, posting lists and token frequencies
	tmpFields, err := m.CreateTemp("tmp_fields_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for fields: %w", err)
	}
	defer CloseAndRemove(tmpFields)
	tmpPostingLists, err := m.CreateTemp("tmp_postinglists_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for posting lists: %w", err)
	}
	defer CloseAndRemove(tmpPostingLists)
	tmpTokenFreqs, err := m.CreateTemp("tmp_tokenfreqs_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for token frequencies: %w", err)
	}
	defer CloseAndRemove(tmpTokenFreqs)

	var plBuffer bytes.Buffer
	var fieldCollisions = make([]uint64, 0, len(a.Fields))

	// Phase 2, write B's only fields
	for fieldHash, field := range a.Fields {
		_, found := b.Fields[fieldHash]
		if found {
			// Do not process collision fields yet
			fieldCollisions = append(fieldCollisions, fieldHash)
			continue
		}

		// Write field header to temporary fields file
		data := binary.NativeEndian.AppendUint64(buffer, fieldHash)
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, *(*uint64)(unsafe.Pointer(&field.AvgDocumentLength)))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(field.Tokens.Len()))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(len(field.DocumentLengths)))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
		}

		for index := range field.DocumentLengths {
			docLength := &field.DocumentLengths[index]

			data := binary.NativeEndian.AppendUint64(buffer, docLength.Index)
			_, err = tmpFields.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's document length index: %w: %d:%d", err, fieldHash, docLength.Index)
			}
			data = binary.NativeEndian.AppendUint64(buffer, docLength.Length)
			_, err = tmpFields.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, docLength.Index)
			}
		}

		// Write posting lists
		it := field.Tokens.Iter()
		err = func() (err error) {
			defer it.Release()
			for valid := it.First(); valid; valid = it.Next() {
				token := it.Item()

				data := binary.NativeEndian.AppendUint64(buffer, token.DocumentFrequencyCount)
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token document frequency: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Add posting list
				data = binary.NativeEndian.AppendUint64(buffer, postingListsCursor)
				postingListsCursor++
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list index: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Write directly to the posting lists temporary file
				plBuffer.Reset()
				postingList := &a.PostingLists[token.PostingListIndex]
				size := postingList.GetSerializedSizeInBytes()
				plBuffer.Grow(int(size))
				postingList.WriteTo(&plBuffer)

				data = binary.NativeEndian.AppendUint64(buffer, size)
				_, err = tmpPostingLists.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, token.Value)
				}
				_, err = tmpPostingLists.Write(plBuffer.Bytes())
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Add token frequency
				data = binary.NativeEndian.AppendUint64(buffer, freqsCursor)
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value)
				}
				freqsCursor += token.DocumentFrequencyCount

				// Write directly to frequencies temporary file
				freqs := a.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.DocumentFrequencyCount]
				for index := range freqs {

					freq := &freqs[index]

					data := binary.NativeEndian.AppendUint64(buffer, freq.DocumentIndex)
					_, err = tmpTokenFreqs.Write(data)
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency document index: %w: %d:%s", err, fieldHash, token.Value)
					}
					data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
					_, err = tmpTokenFreqs.Write(data)
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value)
					}
				}

				// Write the actual token
				data = binary.NativeEndian.AppendUint16(buffer, uint16(len(token.Value)))
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token value length: %w: %d:%s", err, fieldHash, token.Value)
				}
				data = append(buffer, 0, 0, 0, 0, 0, 0) // Padding 6 bytes
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token value length padding: %w: %d:%s", err, fieldHash, token.Value)
				}
				_, err = tmpFields.Write(token.Value)
				if err != nil {
					return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value)
				}
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("failed to iter over field tokens: %w", err)
		}
	}

	var reusableBitmap = roaring64.New()

	// Phase 3, write B's only fields
	for fieldHash, field := range b.Fields {
		_, found := a.Fields[fieldHash]
		if found {
			continue
		}

		// Write field header to temporary fields file
		data := binary.NativeEndian.AppendUint64(buffer, fieldHash)
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, *(*uint64)(unsafe.Pointer(&field.AvgDocumentLength)))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(field.Tokens.Len()))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(len(field.DocumentLengths)))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
		}

		for index := range field.DocumentLengths {
			docLength := &field.DocumentLengths[index]

			data := binary.NativeEndian.AppendUint64(buffer, docOffset+docLength.Index)
			_, err = tmpFields.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's document length index: %w: %d:%d", err, fieldHash, docLength.Index)
			}
			data = binary.NativeEndian.AppendUint64(buffer, docLength.Length)
			_, err = tmpFields.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, docLength.Index)
			}
		}

		// Write posting lists
		it := field.Tokens.Iter()
		err = func() (err error) {
			defer it.Release()

			for valid := it.First(); valid; valid = it.Next() {
				token := it.Item()

				data := binary.NativeEndian.AppendUint64(buffer, token.DocumentFrequencyCount)
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token document frequency: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Add posting list
				data = binary.NativeEndian.AppendUint64(buffer, postingListsCursor)
				postingListsCursor++
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list index: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Write directly to the posting lists temporary file
				plBuffer.Reset()
				reusableBitmap.Clear()
				for it := b.PostingLists[token.PostingListIndex].Iterator(); it.HasNext(); {
					reusableBitmap.Add(docOffset + it.Next())
				}
				size := reusableBitmap.GetSerializedSizeInBytes()
				plBuffer.Grow(int(size))
				reusableBitmap.WriteTo(&plBuffer)

				data = binary.NativeEndian.AppendUint64(buffer, size)
				_, err = tmpPostingLists.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, token.Value)
				}
				_, err = tmpPostingLists.Write(plBuffer.Bytes())
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Add token frequency
				data = binary.NativeEndian.AppendUint64(buffer, freqsCursor)
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value)
				}
				freqsCursor += token.DocumentFrequencyCount

				// Write directly to frequencies temporary file
				freqs := b.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.DocumentFrequencyCount]
				for index := range freqs {

					freq := &freqs[index]

					data := binary.NativeEndian.AppendUint64(buffer, docOffset+freq.DocumentIndex)
					_, err = tmpTokenFreqs.Write(data)
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency document index: %w: %d:%s", err, fieldHash, token.Value)
					}
					data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
					_, err = tmpTokenFreqs.Write(data)
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value)
					}
				}

				// Write the actual token
				data = binary.NativeEndian.AppendUint16(buffer, uint16(len(token.Value)))
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token value length: %w: %d:%s", err, fieldHash, token.Value)
				}
				data = append(buffer, 0, 0, 0, 0, 0, 0)
				_, err = tmpFields.Write(data) // Padding 6 bytes
				if err != nil {
					return fmt.Errorf("failed to write B's field token value length padding: %w: %d:%s", err, fieldHash, token.Value)
				}
				_, err = tmpFields.Write(token.Value)
				if err != nil {
					return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value)
				}
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("failed to iter over field tokens: %w", err)
		}
	}

	// Phase 4, add collision fields
	for _, fieldHash := range fieldCollisions {
		fieldA := a.Fields[fieldHash]
		fieldB := b.Fields[fieldHash]
		var collisionTokensReferencedFromA = make([]*Token, 0, fieldA.Tokens.Len())

		// We need to maintain all fields sorted prior write
		// So it is impossible to not store something in memory at least meanwhile
		var finalTokens = btree.NewBTreeG(TokenLessFunc)
		preallocTokens := make([]Token, fieldA.Tokens.Len()+fieldB.Tokens.Len())

		it := fieldA.Tokens.Iter()
		err = func() (err error) {
			defer it.Release()

			for valid := it.First(); valid; valid = it.Next() {
				token := it.Item()

				_, found := fieldB.Tokens.Get(token)
				if found {
					collisionTokensReferencedFromA = append(collisionTokensReferencedFromA, token)
					continue
				}

				newToken := &preallocTokens[0]
				preallocTokens = preallocTokens[1:]

				*newToken = *token
				newToken.PostingListIndex = postingListsCursor
				postingListsCursor++
				newToken.FrequenciesIndex = freqsCursor
				freqsCursor += newToken.DocumentFrequencyCount

				// Add token to the pending for insertion
				finalTokens.Set(newToken)

				// Write the posting list
				plBuffer.Reset()
				postingList := &a.PostingLists[token.PostingListIndex]
				size := postingList.GetSerializedSizeInBytes()
				plBuffer.Grow(int(size))
				postingList.WriteTo(&plBuffer)

				data := binary.NativeEndian.AppendUint64(buffer, size)
				_, err = tmpPostingLists.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write A's field token posting list size: %w: %d:%s", err, fieldHash, token.Value)
				}
				_, err = tmpPostingLists.Write(plBuffer.Bytes())
				if err != nil {
					return fmt.Errorf("failed to write A's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Write the frequencies
				freqs := a.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.DocumentFrequencyCount]
				for index := range freqs {

					freq := &freqs[index]

					data := binary.NativeEndian.AppendUint64(buffer, freq.DocumentIndex)
					_, err = tmpTokenFreqs.Write(data)
					if err != nil {
						return fmt.Errorf("failed to write A's field token frequency document index: %w: %d:%s", err, fieldHash, token.Value)
					}
					data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
					_, err = tmpTokenFreqs.Write(data)
					if err != nil {
						return fmt.Errorf("failed to write A's field token frequency: %w: %d:%s", err, fieldHash, token.Value)
					}
				}
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("failed to iterate over field's A tokens: %w", err)
		}

		it = fieldB.Tokens.Iter()
		err = func() (err error) {
			defer it.Release()

			for valid := it.First(); valid; valid = it.Next() {
				token := it.Item()

				_, found := fieldA.Tokens.Get(token)
				if found {
					continue
				}

				newToken := &preallocTokens[0]
				preallocTokens = preallocTokens[1:]

				*newToken = *token
				newToken.PostingListIndex = postingListsCursor
				postingListsCursor++
				newToken.FrequenciesIndex = freqsCursor
				freqsCursor += newToken.DocumentFrequencyCount

				// Add token to the pending for insertion
				finalTokens.Set(newToken)

				// Write the posting list
				plBuffer.Reset()
				reusableBitmap.Clear()
				for it := b.PostingLists[token.PostingListIndex].Iterator(); it.HasNext(); {
					bDocId := docOffset + it.Next()
					reusableBitmap.Add(bDocId)
				}
				size := reusableBitmap.GetSerializedSizeInBytes()
				plBuffer.Grow(int(size))
				reusableBitmap.WriteTo(&plBuffer)

				data := binary.NativeEndian.AppendUint64(buffer, size)
				_, err = tmpPostingLists.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, token.Value)
				}
				_, err = tmpPostingLists.Write(plBuffer.Bytes())
				if err != nil {
					return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Write the frequencies
				freqs := b.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.DocumentFrequencyCount]
				for index := range freqs {

					freq := &freqs[index]

					data := binary.NativeEndian.AppendUint64(buffer, docOffset+freq.DocumentIndex)
					_, err = tmpTokenFreqs.Write(data)
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency document index: %w: %d:%s", err, fieldHash, token.Value)
					}
					data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
					_, err = tmpTokenFreqs.Write(data)
					if err != nil {
						return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value)
					}
				}
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("failed to iterate over field's B tokens: %w", err)
		}

		for _, tokenA := range collisionTokensReferencedFromA {
			reusableBitmap.Clear()

			tokenB, _ := fieldB.Tokens.Get(tokenA)

			newToken := &preallocTokens[0]
			preallocTokens = preallocTokens[1:]

			*newToken = *tokenA
			newToken.DocumentFrequencyCount = tokenA.DocumentFrequencyCount + tokenB.DocumentFrequencyCount
			newToken.PostingListIndex = postingListsCursor
			postingListsCursor++
			newToken.FrequenciesIndex = freqsCursor
			freqsCursor += newToken.DocumentFrequencyCount

			// Add token to the pending for insertion
			finalTokens.Set(newToken)

			bitmapA := a.PostingLists[tokenA.PostingListIndex]
			bitmapB := b.PostingLists[tokenB.PostingListIndex]

			reusableBitmap.Or(&bitmapA.Bitmap)
			for it := bitmapB.Iterator(); it.HasNext(); {
				bDocId := docOffset + it.Next()
				reusableBitmap.Add(bDocId)
			}

			// Write the posting list
			plBuffer.Reset()
			size := reusableBitmap.GetSerializedSizeInBytes()
			plBuffer.Grow(int(size))
			reusableBitmap.WriteTo(&plBuffer)

			data := binary.NativeEndian.AppendUint64(buffer, size)
			_, err = tmpPostingLists.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write Collision field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value)
			}
			_, err = tmpPostingLists.Write(plBuffer.Bytes())
			if err != nil {
				return fmt.Errorf("failed to write Collision field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value)
			}

			// Write the frequencies
			freqsA := a.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.DocumentFrequencyCount]
			freqsB := b.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.DocumentFrequencyCount]

			freqs := make([]TokenFrequencyEntry, 0, len(freqsA)+len(freqsB))
			freqs = append(freqs, freqsA...)
			for index := range freqsB {
				freq := &freqsB[index]

				freqs = append(freqs, TokenFrequencyEntry{
					DocumentIndex: docOffset + freq.DocumentIndex,
					Frequency:     freq.Frequency,
				})
			}

			for index := range freqs {

				freq := &freqs[index]

				data := binary.NativeEndian.AppendUint64(buffer, freq.DocumentIndex)
				_, err = tmpTokenFreqs.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision field token frequency document index: %w: %d:%s", err, fieldHash, tokenA.Value)
				}
				data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
				_, err = tmpTokenFreqs.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision field token frequency: %w: %d:%s", err, fieldHash, tokenA.Value)
				}
			}
		}

		// Prepare documents lengths
		var totalDocumentLengths uint64
		var documentsLengths = make([]DocumentLengthEntry, 0, len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths))

		for index := range fieldA.DocumentLengths {

			dl := &fieldA.DocumentLengths[index]

			totalDocumentLengths += dl.Length
			documentsLengths = append(documentsLengths, *dl)
		}
		for index := range fieldB.DocumentLengths {
			dl := &fieldB.DocumentLengths[index]

			totalDocumentLengths += dl.Length
			documentsLengths = append(documentsLengths, DocumentLengthEntry{
				Index:  docOffset + dl.Index,
				Length: dl.Length,
			})
		}

		var avgDocumentLength = float64(totalDocumentLengths) / float64(len(documentsLengths))

		// Write the field
		// Write field header to temporary fields file
		data := binary.NativeEndian.AppendUint64(buffer, fieldHash)
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, *(*uint64)(unsafe.Pointer(&avgDocumentLength)))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(finalTokens.Len()))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(len(documentsLengths)))
		_, err = tmpFields.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
		}

		// Write documents lengths
		for index := range documentsLengths {
			docLength := &documentsLengths[index]

			data := binary.NativeEndian.AppendUint64(buffer, docLength.Index)
			_, err = tmpFields.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write Collision document length index: %w: %d:%d", err, fieldHash, docLength.Index)
			}
			data = binary.NativeEndian.AppendUint64(buffer, docLength.Length)
			_, err = tmpFields.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write Collision document length length: %w: %d:%d", err, fieldHash, docLength.Index)
			}
		}

		// Write the final state of tokens
		it = finalTokens.Iter()
		err = func() (err error) {
			defer it.Release()

			// Write tokens into field's file
			for valid := it.First(); valid; valid = it.Next() {
				token := it.Item()

				data := binary.NativeEndian.AppendUint64(buffer, token.DocumentFrequencyCount)
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision field token document frequency: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Add posting list cursor
				data = binary.NativeEndian.AppendUint64(buffer, token.PostingListIndex)
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision field token posting list index: %w: %d:%s", err, fieldHash, token.Value)
				}

				// Add freqs index
				data = binary.NativeEndian.AppendUint64(buffer, token.FrequenciesIndex)
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value)
				}

				data = binary.NativeEndian.AppendUint16(buffer, uint16(len(token.Value)))
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token value length: %w: %d:%s", err, fieldHash, token.Value)
				}
				data = append(buffer, 0, 0, 0, 0, 0, 0) // Padding 6 bytes
				_, err = tmpFields.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token value length padding: %w: %d:%s", err, fieldHash, token.Value)
				}
				_, err = tmpFields.Write(token.Value)
				if err != nil {
					return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value)
				}
			}
			return nil
		}()
		if err != nil {
			return fmt.Errorf("failed to iterate over field's final state tokens: %w", err)
		}
	}

	// Phase 5, Assembly everything
	file, err := os.Create(name)
	if err != nil {
		return fmt.Errorf("failed to create destination file: %w", err)
	}
	defer file.Close()

	// File Header
	data := binary.NativeEndian.AppendUint64(buffer, MagicNumber)
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write header magic number: %w ", err)
	}
	data = binary.NativeEndian.AppendUint16(buffer, VersionV1)
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write header version: %w ", err)
	}
	data = append(buffer, 0, 0, 0, 0, 0, 0) // Padding 6 bytes
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write header padding: %w ", err)
	}
	data = binary.NativeEndian.AppendUint64(buffer, uint64(len(a.DocumentsIds))+uint64(len(b.DocumentsIds)))
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write header docs ids count: %w ", err)
	}
	data = binary.NativeEndian.AppendUint64(buffer, uint64(len(a.Fields))+uint64(len(b.Fields))-uint64(len(fieldCollisions)))
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write header fields count: %w ", err)
	}
	data = binary.NativeEndian.AppendUint64(buffer, postingListsCursor)
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write header posting lists count: %w ", err)
	}
	data = binary.NativeEndian.AppendUint64(buffer, freqsCursor)
	_, err = file.Write(data)
	if err != nil {
		return fmt.Errorf("failed to write header freqs count: %w ", err)
	}

	tmpDocIds.Seek(0, 0)
	tmpFields.Seek(0, 0)
	tmpPostingLists.Seek(0, 0)
	tmpTokenFreqs.Seek(0, 0)

	// Hopefully all these calls will use send file or splice internally :)
	_, err = file.ReadFrom(tmpDocIds)
	if err != nil {
		return fmt.Errorf("failed to append doc ids: %w", err)
	}
	_, err = file.ReadFrom(tmpFields)
	if err != nil {
		return fmt.Errorf("failed to append fields: %w", err)
	}
	_, err = file.ReadFrom(tmpPostingLists)
	if err != nil {
		return fmt.Errorf("failed to append posting lists: %w", err)
	}
	_, err = file.ReadFrom(tmpTokenFreqs)
	if err != nil {
		return fmt.Errorf("failed to append token freqs: %w", err)
	}

	return nil
}
