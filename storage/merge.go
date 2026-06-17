package storage

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"unsafe"

	"github.com/RoaringBitmap/roaring/roaring64"
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

	tmpDocIdsFile, err := m.CreateTemp("tmp_docids_*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary file for documents ids: %w", err)
	}
	defer CloseAndRemove(tmpDocIdsFile)

	docIdsW := bufio.NewWriterSize(tmpDocIdsFile, 2<<20)

	// Phase 1, write document ids to temporary file
	for docIdIdx := range a.DocumentsIds {
		docId := &a.DocumentsIds[docIdIdx]
		data := binary.NativeEndian.AppendUint64(buffer, docId.Value.Size)
		_, err = docIdsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write A's document id length: %w: %s", err, docId.Value.UnsafeString())
		}

		_, err = docIdsW.Write(docId.Value.Data[:])
		if err != nil {
			return fmt.Errorf("failed to write A's document id: %w: %s", err, docId.Value.UnsafeString())
		}
	}

	for docIdIdx := range b.DocumentsIds {
		docId := &b.DocumentsIds[docIdIdx]
		data := binary.NativeEndian.AppendUint64(buffer, docId.Value.Size)
		_, err = docIdsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's document id length: %w: %s", err, docId.Value.UnsafeString())
		}

		_, err = docIdsW.Write(docId.Value.Data[:])
		if err != nil {
			return fmt.Errorf("failed to write B's document id: %w: %s", err, docId.Value.UnsafeString())
		}
	}

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
		_, err = fieldsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, *(*uint64)(unsafe.Pointer(&field.AvgDocumentLength)))
		_, err = fieldsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(len(field.Tokens)))
		_, err = fieldsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(len(field.DocumentLengths)))
		_, err = fieldsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
		}

		for index := range field.DocumentLengths {
			docLength := &field.DocumentLengths[index]

			data := binary.NativeEndian.AppendUint64(buffer, docLength.Index)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's document length index: %w: %d:%d", err, fieldHash, docLength.Index)
			}
			data = binary.NativeEndian.AppendUint64(buffer, docLength.Length)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, docLength.Index)
			}
		}

		// Write posting lists
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			data := binary.NativeEndian.AppendUint64(buffer, token.FrequencyCount)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add posting list
			data = binary.NativeEndian.AppendUint64(buffer, postingListsCursor)
			postingListsCursor++
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Write directly to the posting lists temporary file
			plBuffer.Reset()
			postingList := &a.PostingLists[token.PostingListIndex]
			size := postingList.GetSerializedSizeInBytes()
			plBuffer.Grow(int(size))
			postingList.WriteTo(&plBuffer)

			data = binary.NativeEndian.AppendUint64(buffer, size)
			_, err = postingsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			_, err = postingsW.Write(plBuffer.Bytes())
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add token frequency
			data = binary.NativeEndian.AppendUint64(buffer, freqsCursor)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			freqsCursor += token.FrequencyCount

			// Write directly to frequencies temporary file
			freqs := a.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]
			for index := range freqs {

				freq := &freqs[index]

				data := binary.NativeEndian.AppendUint64(buffer, freq.DocumentIndex)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency document index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
				}
				data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
				}
			}

			// Write the actual token
			data = binary.NativeEndian.AppendUint64(buffer, token.Value.Size)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token value length: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			_, err = fieldsW.Write(token.Value.Data[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
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
		_, err = fieldsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, *(*uint64)(unsafe.Pointer(&field.AvgDocumentLength)))
		_, err = fieldsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(len(field.Tokens)))
		_, err = fieldsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
		}
		data = binary.NativeEndian.AppendUint64(buffer, uint64(len(field.DocumentLengths)))
		_, err = fieldsW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's documents lengths: %w: %d", err, fieldHash)
		}

		for index := range field.DocumentLengths {
			docLength := &field.DocumentLengths[index]

			data := binary.NativeEndian.AppendUint64(buffer, docOffset+docLength.Index)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's document length index: %w: %d:%d", err, fieldHash, docLength.Index)
			}
			data = binary.NativeEndian.AppendUint64(buffer, docLength.Length)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's document length length: %w: %d:%d", err, fieldHash, docLength.Index)
			}
		}

		// Write posting lists
		for tokenIdx := range field.Tokens {
			token := &field.Tokens[tokenIdx]

			data := binary.NativeEndian.AppendUint64(buffer, token.FrequencyCount)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token document frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add posting list
			data = binary.NativeEndian.AppendUint64(buffer, postingListsCursor)
			postingListsCursor++
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
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
			_, err = postingsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			_, err = postingsW.Write(plBuffer.Bytes())
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}

			// Add token frequency
			data = binary.NativeEndian.AppendUint64(buffer, freqsCursor)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			freqsCursor += token.FrequencyCount

			// Write directly to frequencies temporary file
			freqs := b.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]
			for index := range freqs {

				freq := &freqs[index]

				data := binary.NativeEndian.AppendUint64(buffer, docOffset+freq.DocumentIndex)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency document index: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
				}
				data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
				}
			}

			// Write the actual token
			data = binary.NativeEndian.AppendUint64(buffer, token.Value.Size)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token value length: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
			_, err = fieldsW.Write(token.Value.Data[:])
			if err != nil {
				return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, token.Value.UnsafeString())
			}
		}
	}

	visitedTokens := make(map[uint64]struct{})
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

			bitmapA := a.PostingLists[tokenA.PostingListIndex]
			bitmapB := b.PostingLists[tokenB.PostingListIndex]

			reusableBitmap.Clear()
			reusableBitmap.Or(&bitmapA.Bitmap)
			for it := bitmapB.Iterator(); it.HasNext(); {
				reusableBitmap.Add(docOffset + it.Next())
			}

			// Write the posting list
			plBuffer.Reset()
			size := reusableBitmap.GetSerializedSizeInBytes()
			plBuffer.Grow(int(size))
			reusableBitmap.WriteTo(&plBuffer)

			data := binary.NativeEndian.AppendUint64(buffer, size)
			_, err = postingsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write Collision field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
			}
			_, err = postingsW.Write(plBuffer.Bytes())
			if err != nil {
				return fmt.Errorf("failed to write Collision field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
			}

			// Write the frequencies
			freqsA := a.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
			freqsB := b.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]

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
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision field token frequency document index: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
				}
				data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision field token frequency: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
				}
			}
		case tokenA != nil:
			finalToken = *tokenA
			finalToken.PostingListIndex = postingListsCursor
			postingListsCursor++
			finalToken.FrequenciesIndex = freqsCursor
			freqsCursor += finalToken.FrequencyCount

			// Write the posting list
			plBuffer.Reset()
			postingList := &a.PostingLists[tokenA.PostingListIndex]
			size := postingList.GetSerializedSizeInBytes()
			plBuffer.Grow(int(size))
			postingList.WriteTo(&plBuffer)

			data := binary.NativeEndian.AppendUint64(buffer, size)
			_, err = postingsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write A's field token posting list size: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
			}
			_, err = postingsW.Write(plBuffer.Bytes())
			if err != nil {
				return fmt.Errorf("failed to write A's field token posting list contents: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
			}

			// Write the frequencies
			freqs := a.TokenFrequencies[tokenA.FrequenciesIndex : tokenA.FrequenciesIndex+tokenA.FrequencyCount]
			for index := range freqs {

				freq := &freqs[index]

				data := binary.NativeEndian.AppendUint64(buffer, freq.DocumentIndex)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write A's field token frequency document index: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
				}
				data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write A's field token frequency: %w: %d:%s", err, fieldHash, tokenA.Value.UnsafeString())
				}
			}
		case tokenB != nil:
			finalToken = *tokenB
			finalToken.PostingListIndex = postingListsCursor
			postingListsCursor++
			finalToken.FrequenciesIndex = freqsCursor
			freqsCursor += finalToken.FrequencyCount

			// Write the posting list
			plBuffer.Reset()
			reusableBitmap.Clear()
			for it := b.PostingLists[tokenB.PostingListIndex].Iterator(); it.HasNext(); {
				bDocId := docOffset + it.Next()
				reusableBitmap.Add(bDocId)
			}
			size := reusableBitmap.GetSerializedSizeInBytes()
			plBuffer.Grow(int(size))
			reusableBitmap.WriteTo(&plBuffer)

			data := binary.NativeEndian.AppendUint64(buffer, size)
			_, err = postingsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list size: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
			}
			_, err = postingsW.Write(plBuffer.Bytes())
			if err != nil {
				return fmt.Errorf("failed to write B's field token posting list contents: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
			}

			// Write the frequencies
			freqs := b.TokenFrequencies[tokenB.FrequenciesIndex : tokenB.FrequenciesIndex+tokenB.FrequencyCount]
			for index := range freqs {

				freq := &freqs[index]

				data := binary.NativeEndian.AppendUint64(buffer, docOffset+freq.DocumentIndex)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency document index: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
				}
				data = binary.NativeEndian.AppendUint64(buffer, freq.Frequency)
				_, err = tokenFreqsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write B's field token frequency: %w: %d:%s", err, fieldHash, tokenB.Value.UnsafeString())
				}
			}
		}

		data := binary.NativeEndian.AppendUint64(buffer, finalToken.FrequencyCount)
		_, err = fieldTokensW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write Collision field token document frequency: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
		}

		// Add posting list cursor
		data = binary.NativeEndian.AppendUint64(buffer, finalToken.PostingListIndex)
		_, err = fieldTokensW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write Collision field token posting list index: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
		}

		// Add freqs index
		data = binary.NativeEndian.AppendUint64(buffer, finalToken.FrequenciesIndex)
		_, err = fieldTokensW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field token frequencies index: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
		}

		data = binary.NativeEndian.AppendUint64(buffer, finalToken.Value.Size)
		_, err = fieldTokensW.Write(data)
		if err != nil {
			return fmt.Errorf("failed to write B's field token value length: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
		}
		_, err = fieldTokensW.Write(finalToken.Value.Data[:])
		if err != nil {
			return fmt.Errorf("failed to write B's field token value: %w: %d:%s", err, fieldHash, finalToken.Value.UnsafeString())
		}
		return nil
	}

	tmpFieldTokensFile, err := m.CreateTemp("field-tokens-*.part")
	if err != nil {
		return fmt.Errorf("failed to create temporary field tokens file: %w", err)
	}
	defer func() {
		tmpFieldTokensFile.Close()
		os.Remove(tmpFieldTokensFile.Name())
	}()

	fieldTokensW := bufio.NewWriterSize(tmpFieldTokensFile, 2<<20)

	tmpFieldTokenDocLengths, err := m.CreateTemp("field-tokens-doc-lengths-*")
	if err != nil {
		return fmt.Errorf("failed to create temporary field tokens doc lengths file: %w", err)
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
			return fmt.Errorf("failed to retruncate field tokens file: %w", err)
		}

		fieldTokensW.Reset(tmpFieldTokensFile)

		finalTokensCount = 0
		err := func() (err error) {

			clear(visitedTokens)

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

				data := binary.NativeEndian.AppendUint64(buffer, dl.Index)
				_, err = fieldTokenDocLengthsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision document length index: %w: %d:%d", err, fieldHash, dl.Index)
				}
				data = binary.NativeEndian.AppendUint64(buffer, dl.Length)
				_, err = fieldTokenDocLengthsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision document length length: %w: %d:%d", err, fieldHash, dl.Index)
				}
			}
			for index := range fieldB.DocumentLengths {
				dl := &fieldB.DocumentLengths[index]

				totalDocumentLengths += dl.Length
				data := binary.NativeEndian.AppendUint64(buffer, docOffset+dl.Index)
				_, err = fieldTokenDocLengthsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision document length index: %w: %d:%d", err, fieldHash, dl.Index)
				}
				data = binary.NativeEndian.AppendUint64(buffer, dl.Length)
				_, err = fieldTokenDocLengthsW.Write(data)
				if err != nil {
					return fmt.Errorf("failed to write Collision document length length: %w: %d:%d", err, fieldHash, dl.Index)
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
			data := binary.NativeEndian.AppendUint64(buffer, fieldHash)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field hash: %w: %d", err, fieldHash)
			}
			data = binary.NativeEndian.AppendUint64(buffer, *(*uint64)(unsafe.Pointer(&avgDocumentLength)))
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's field avgdl: %w: %d", err, fieldHash)
			}
			data = binary.NativeEndian.AppendUint64(buffer, finalTokensCount)
			_, err = fieldsW.Write(data)
			if err != nil {
				return fmt.Errorf("failed to write B's tokens length: %w: %d", err, fieldHash)
			}
			data = binary.NativeEndian.AppendUint64(buffer, uint64(len(fieldA.DocumentLengths)+len(fieldB.DocumentLengths)))
			_, err = fieldsW.Write(data)
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
			return fmt.Errorf("failed to handle field hash: %w", err)
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

	docIdsW.Flush()
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
