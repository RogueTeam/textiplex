package storage

import (
	"bytes"
	"encoding/binary"
	"slices"
	"unsafe"

	"github.com/RoaringBitmap/roaring/roaring64"
)

// CalculateMergeSize computes the exact byte size of the merged storage
// without performing the merge. The caller uses this to Truncate a file
// and mmap it before calling Merge.
//
// Assumes disjoint ordered doc ID ranges: every doc ID in b sorts after
// every doc ID in a. This is guaranteed when the caller partitions the
// corpus by sorted doc ID range before building each batch.
func CalculateMergeSize(a, b *Storage) uint64 {
	size := uint64(HeaderSize)

	// doc ID table — all docs from both, no overlap by assumption
	for _, id := range a.DocumentsIds {
		size += uint64(DocumentIdHeaderSize) + uint64(len(id))
	}
	for _, id := range b.DocumentsIds {
		size += uint64(DocumentIdHeaderSize) + uint64(len(id))
	}

	// union of field hashes
	fieldHashes := mergedFieldHashes(a, b)

	for hash := range fieldHashes {
		size += uint64(FieldHeaderSize)

		fa := a.Fields[hash]
		fb := b.Fields[hash]

		if fa != nil {
			size += uint64(DocumentLengthEntrySize) * uint64(len(fa.DocumentLengths))
		}
		if fb != nil {
			size += uint64(DocumentLengthEntrySize) * uint64(len(fb.DocumentLengths))
		}

		walkMergedTokens(a, b, fa, fb, func(kind mergeKind, tokA, tokB *Token) {
			switch kind {
			case mergeOnlyA:
				size += uint64(TokenHeaderSize) + uint64(len(tokA.Value))
				size += uint64(PostingListHeaderSize)
				size += a.PostingLists[tokA.PostingListIndex].GetSerializedSizeInBytes()
				size += uint64(TokenFrequencyEntrySize) * tokA.DocumentFrequencyCount

			case mergeOnlyB:
				size += uint64(TokenHeaderSize) + uint64(len(tokB.Value))
				size += uint64(PostingListHeaderSize)
				size += b.PostingLists[tokB.PostingListIndex].GetSerializedSizeInBytes()
				size += uint64(TokenFrequencyEntrySize) * tokB.DocumentFrequencyCount

			case mergeBoth:
				// need actual merged bitmap to get serialized size
				merged := roaring64.Or(
					&a.PostingLists[tokA.PostingListIndex].Bitmap,
					&b.PostingLists[tokB.PostingListIndex].Bitmap,
				)
				size += uint64(TokenHeaderSize) + uint64(len(tokA.Value))
				size += uint64(PostingListHeaderSize)
				size += merged.GetSerializedSizeInBytes()
				size += uint64(TokenFrequencyEntrySize) * (tokA.DocumentFrequencyCount + tokB.DocumentFrequencyCount)
			}
		})
	}

	return size
}

// Merge writes the merged contents of a and b into dst and returns the
// filled slice. dst should be pre-allocated to CalculateMergeSize(a, b)
// bytes — typically via file Truncate + mmap.
//
// Assumes disjoint ordered doc ID ranges: every doc ID in b sorts after
// every doc ID in a. b's internal doc indices are shifted by
// len(a.DocumentsIds) so they remain valid sequential integers in the
// merged storage.
//
// Call LoadBytes on the returned slice to get a queryable Storage.
func Merge(dst []byte, a, b *Storage) []byte {
	out := dst
	offset := uint64(len(a.DocumentsIds))

	// ── Count totals for header ───────────────────────────────────────────

	fieldHashes := mergedFieldHashes(a, b)

	totalPL := uint64(0)
	totalTF := uint64(0)
	for hash := range fieldHashes {
		walkMergedTokens(a, b, a.Fields[hash], b.Fields[hash], func(kind mergeKind, tokA, tokB *Token) {
			totalPL++
			switch kind {
			case mergeOnlyA:
				totalTF += tokA.DocumentFrequencyCount
			case mergeOnlyB:
				totalTF += tokB.DocumentFrequencyCount
			case mergeBoth:
				totalTF += tokA.DocumentFrequencyCount + tokB.DocumentFrequencyCount
			}
		})
	}

	totalDocs := uint64(len(a.DocumentsIds) + len(b.DocumentsIds))

	// ── Header ───────────────────────────────────────────────────────────

	out = binary.NativeEndian.AppendUint64(out, MagicNumber)
	out = binary.NativeEndian.AppendUint16(out, VersionV1)
	out = append(out, 0, 0, 0, 0, 0, 0) // padding
	out = binary.NativeEndian.AppendUint64(out, totalDocs)
	out = binary.NativeEndian.AppendUint64(out, uint64(len(fieldHashes)))
	out = binary.NativeEndian.AppendUint64(out, totalPL)
	out = binary.NativeEndian.AppendUint64(out, totalTF)

	// ── Doc ID table ──────────────────────────────────────────────────────

	for _, id := range a.DocumentsIds {
		out = binary.NativeEndian.AppendUint16(out, uint16(len(id)))
		out = append(out, id...)
	}
	for _, id := range b.DocumentsIds {
		out = binary.NativeEndian.AppendUint16(out, uint16(len(id)))
		out = append(out, id...)
	}

	// ── Field blocks, posting lists, TF region ────────────────────────────
	// Field blocks must reference posting list and TF indices, but those
	// regions come after field blocks in the file. We buffer field blocks
	// separately and append them before the two regions.

	sortedHashes := make([]uint64, 0, len(fieldHashes))
	for h := range fieldHashes {
		sortedHashes = append(sortedHashes, h)
	}
	slices.Sort(sortedHashes)

	type plEntry struct{ bitmap *roaring64.Bitmap }
	plRegion := make([]plEntry, 0, totalPL)
	tfRegion := make([]TokenFrequencyEntry, 0, totalTF)

	var fieldBuf []byte

	for _, hash := range sortedHashes {
		fa := a.Fields[hash]
		fb := b.Fields[hash]

		// merged doc lengths
		var mergedDL []DocumentLengthEntry
		switch {
		case fa != nil && fb != nil:
			mergedDL = make([]DocumentLengthEntry, 0, len(fa.DocumentLengths)+len(fb.DocumentLengths))
			mergedDL = append(mergedDL, fa.DocumentLengths...)
			for _, dl := range fb.DocumentLengths {
				mergedDL = append(mergedDL, DocumentLengthEntry{
					Index:  dl.Index + offset,
					Length: dl.Length,
				})
			}
		case fa != nil:
			mergedDL = fa.DocumentLengths
		default:
			mergedDL = make([]DocumentLengthEntry, len(fb.DocumentLengths))
			for i, dl := range fb.DocumentLengths {
				mergedDL[i] = DocumentLengthEntry{Index: dl.Index + offset, Length: dl.Length}
			}
		}

		// avgdl from merged doc lengths
		var totalLen, docCount uint64
		for _, dl := range mergedDL {
			totalLen += dl.Length
			docCount++
		}
		var avgdl float64
		if docCount > 0 {
			avgdl = float64(totalLen) / float64(docCount)
		}

		// merged token list with assigned pl/tf indices
		type mergedTok struct {
			value   []byte
			plIndex uint64
			tfIndex uint64
			docFreq uint64
		}
		var mergedTokens []mergedTok

		emit := func(value []byte, bm *roaring64.Bitmap, freqs []TokenFrequencyEntry) {
			plIdx := uint64(len(plRegion))
			tfIdx := uint64(len(tfRegion))
			plRegion = append(plRegion, plEntry{bitmap: bm})
			tfRegion = append(tfRegion, freqs...)
			mergedTokens = append(mergedTokens, mergedTok{
				value:   value,
				plIndex: plIdx,
				tfIndex: tfIdx,
				docFreq: bm.GetCardinality(),
			})
		}

		shiftBitmap := func(bm *roaring64.Bitmap) *roaring64.Bitmap {
			shifted := roaring64.New()
			it := bm.Iterator()
			for it.HasNext() {
				shifted.Add(it.Next() + offset)
			}
			return shifted
		}

		shiftFreqs := func(freqs []TokenFrequencyEntry) []TokenFrequencyEntry {
			result := make([]TokenFrequencyEntry, len(freqs))
			for i, f := range freqs {
				result[i] = TokenFrequencyEntry{
					DocumentIndex: f.DocumentIndex + offset,
					Frequency:     f.Frequency,
				}
			}
			return result
		}

		walkMergedTokens(a, b, fa, fb, func(kind mergeKind, tokA, tokB *Token) {
			switch kind {
			case mergeOnlyA:
				bitmapCopy := a.PostingLists[tokA.PostingListIndex].Bitmap.Clone()
				freqs := a.TokenFrequencies[tokA.FrequenciesIndex : tokA.FrequenciesIndex+tokA.DocumentFrequencyCount]
				emit(tokA.Value, bitmapCopy, freqs)

			case mergeOnlyB:
				shifted := shiftBitmap(&b.PostingLists[tokB.PostingListIndex].Bitmap)
				freqs := shiftFreqs(b.TokenFrequencies[tokB.FrequenciesIndex : tokB.FrequenciesIndex+tokB.DocumentFrequencyCount])
				emit(tokB.Value, shifted, freqs)

			case mergeBoth:
				bitmapA := &a.PostingLists[tokA.PostingListIndex].Bitmap
				bitmapB := shiftBitmap(&b.PostingLists[tokB.PostingListIndex].Bitmap)
				merged := roaring64.Or(bitmapA, bitmapB)

				freqsA := a.TokenFrequencies[tokA.FrequenciesIndex : tokA.FrequenciesIndex+tokA.DocumentFrequencyCount]
				freqsB := shiftFreqs(b.TokenFrequencies[tokB.FrequenciesIndex : tokB.FrequenciesIndex+tokB.DocumentFrequencyCount])
				allFreqs := make([]TokenFrequencyEntry, 0, len(freqsA)+len(freqsB))
				allFreqs = append(allFreqs, freqsA...)
				allFreqs = append(allFreqs, freqsB...)
				emit(tokA.Value, merged, allFreqs)
			}
		})

		// write field header
		fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, hash)
		fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, *(*uint64)(unsafe.Pointer(&avgdl)))
		fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, uint64(len(mergedTokens)))
		fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, uint64(len(mergedDL)))

		for _, dl := range mergedDL {
			fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, dl.Index)
			fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, dl.Length)
		}

		for _, mt := range mergedTokens {
			fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, mt.docFreq)
			fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, mt.plIndex)
			fieldBuf = binary.NativeEndian.AppendUint64(fieldBuf, mt.tfIndex)
			fieldBuf = binary.NativeEndian.AppendUint16(fieldBuf, uint16(len(mt.value)))
			fieldBuf = append(fieldBuf, 0, 0, 0, 0, 0, 0) // padding
			fieldBuf = append(fieldBuf, mt.value...)
		}
	}

	out = append(out, fieldBuf...)

	// ── Posting lists region ──────────────────────────────────────────────

	var plBuf bytes.Buffer
	for _, pl := range plRegion {
		plBuf.Reset()
		size := pl.bitmap.GetSerializedSizeInBytes()
		plBuf.Grow(int(size))
		pl.bitmap.WriteTo(&plBuf)
		out = binary.NativeEndian.AppendUint64(out, size)
		out = append(out, plBuf.Bytes()...)
	}

	// ── TF region ─────────────────────────────────────────────────────────

	for _, tf := range tfRegion {
		out = binary.NativeEndian.AppendUint64(out, tf.DocumentIndex)
		out = binary.NativeEndian.AppendUint64(out, tf.Frequency)
	}

	return out
}

// MergeStorages merges a and b into a fresh in-memory storage.
// Useful for testing and smaller corpora where mmap is not required.
// For production use pre-allocate via CalculateMergeSize + mmap.
func MergeStorages(a, b *Storage) (*Storage, error) {
	size := CalculateMergeSize(a, b)
	dst := make([]byte, 0, size)
	out := Merge(dst, a, b)
	merged := &Storage{}
	if err := merged.LoadBytes(out); err != nil {
		return nil, err
	}
	return merged, nil
}

// ── internal helpers ──────────────────────────────────────────────────────────

type mergeKind uint8

const (
	mergeOnlyA mergeKind = iota
	mergeOnlyB
	mergeBoth
)

func mergedFieldHashes(a, b *Storage) map[uint64]struct{} {
	hashes := make(map[uint64]struct{}, len(a.Fields)+len(b.Fields))
	for h := range a.Fields {
		hashes[h] = struct{}{}
	}
	for h := range b.Fields {
		hashes[h] = struct{}{}
	}
	return hashes
}

// walkMergedTokens iterates both field token indexes simultaneously in
// alphabetical order, calling fn once per unique token value with a kind
// indicating whether the token exists in a only, b only, or both.
// fa or fb may be nil if the field doesn't exist in that storage.
func walkMergedTokens(a, b *Storage, fa, fb *Field, fn func(mergeKind, *Token, *Token)) {
	if fa == nil && fb == nil {
		return
	}
	if fa == nil {
		fb.Tokens.Scan(func(tok *Token) bool {
			fn(mergeOnlyB, nil, tok)
			return true
		})
		return
	}
	if fb == nil {
		fa.Tokens.Scan(func(tok *Token) bool {
			fn(mergeOnlyA, tok, nil)
			return true
		})
		return
	}

	itA := fa.Tokens.Iter()
	itB := fb.Tokens.Iter()
	validA, validB := itA.First(), itB.First()

	for validA || validB {
		var cmp int
		switch {
		case !validA:
			cmp = 1
		case !validB:
			cmp = -1
		default:
			cmp = bytes.Compare(itA.Item().Value, itB.Item().Value)
		}

		switch {
		case cmp < 0:
			fn(mergeOnlyA, itA.Item(), nil)
			validA = itA.Next()
		case cmp > 0:
			fn(mergeOnlyB, nil, itB.Item())
			validB = itB.Next()
		default:
			fn(mergeBoth, itA.Item(), itB.Item())
			validA = itA.Next()
			validB = itB.Next()
		}
	}
	itA.Release()
	itB.Release()
}
