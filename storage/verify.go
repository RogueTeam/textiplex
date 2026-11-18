package storage

import (
	"bytes"
	"fmt"
	"unsafe"

	"github.com/RoaringBitmap/roaring/v2"
	"github.com/zeebo/xxh3"
)

// Verify checks the whole storage and returns an error wrapping ErrCorrupt
// (ErrChecksum for a checksum mismatch) describing the first problem found.
//
// For a loaded version 2 file it first compares the xxh3 checksum of the file
// with the one in its footer. It then checks every internal reference: doc
// ids and tokens fit their buffers, tokens are strictly sorted, every token's
// frequencies and posting list are in range and agree with each other, and
// every document index is below the document count.
//
// Verify reads every byte of the index, so it costs a full pass over the
// file. Load does not call it. Run it after copying an index from another
// machine, at startup if you do not trust the disk, or before an operation
// that destroys the only other copy of the data (Writer.VerifySegments).
func (s *Storage) Verify() (err error) {
	if s.Buffer != nil && s.Version >= VersionV2 {
		if uintptr(len(s.Buffer)) < FooterSize {
			return corruptf("file too small for its footer")
		}
		end := uintptr(len(s.Buffer)) - FooterSize
		footer := (*Footer)(unsafe.Pointer(&s.Buffer[end]))
		if sum := xxh3.Hash(s.Buffer[:end]); sum != footer.Checksum {
			return fmt.Errorf("%w: file hashes to %#x, footer records %#x", ErrChecksum, sum, footer.Checksum)
		}
	}

	documents := uint64(len(s.DocumentsIds))
	for i := range s.DocumentsIds {
		if s.DocumentsIds[i].Value.Truncated() {
			return corruptf("document %d id claims %d bytes", i, s.DocumentsIds[i].Value.Size)
		}
	}

	var bitmap roaring.Bitmap
	for fieldHash, field := range s.Fields {
		var previous uint32
		for i := range field.DocumentLengths {
			entry := &field.DocumentLengths[i]
			if uint64(entry.Index) >= documents {
				return corruptf("field %#x: document length %d references document %d of %d", fieldHash, i, entry.Index, documents)
			}
			if i > 0 && entry.Index <= previous {
				return corruptf("field %#x: document lengths not strictly ascending at %d", fieldHash, i)
			}
			previous = entry.Index
		}

		for i := range field.Tokens {
			token := &field.Tokens[i]
			if token.Value.Truncated() {
				return corruptf("field %#x: token %d claims %d bytes", fieldHash, i, token.Value.Size)
			}
			if i > 0 {
				switch bytes.Compare(field.Tokens[i-1].Value.Bytes(), token.Value.Bytes()) {
				case 0:
					// Also produced by indexes built before values over
					// MaxRawValueSize were rejected: they were cut to 128 bytes.
					return corruptf("field %#x: token %q appears twice (an index built with tokens over %d bytes?)", fieldHash, token.Value.Bytes(), MaxRawValueSize)
				case 1:
					return corruptf("field %#x: tokens not sorted at %d (%q)", fieldHash, i, token.Value.Bytes())
				}
			}

			freqs, err := s.frequenciesOf(token)
			if err != nil {
				return fmt.Errorf("field %#x: %w", fieldHash, err)
			}
			for j := range freqs {
				if uint64(freqs[j].DocumentIndex) >= documents {
					return corruptf("field %#x: token %q references document %d of %d", fieldHash, token.Value.Bytes(), freqs[j].DocumentIndex, documents)
				}
				if j > 0 && freqs[j].DocumentIndex <= freqs[j-1].DocumentIndex {
					return corruptf("field %#x: token %q frequencies not strictly ascending", fieldHash, token.Value.Bytes())
				}
			}

			if err = s.PostingLists.Bitmap(int64(token.PostingListIndex), &bitmap); err != nil {
				return fmt.Errorf("field %#x: token %q: %w", fieldHash, token.Value.Bytes(), err)
			}
			if !bitmap.IsEmpty() && uint64(bitmap.Maximum()) >= documents {
				return corruptf("field %#x: token %q posting list references document %d of %d", fieldHash, token.Value.Bytes(), bitmap.Maximum(), documents)
			}
			if bitmap.GetCardinality() != uint64(len(freqs)) {
				return corruptf("field %#x: token %q posting list has %d documents, frequencies have %d", fieldHash, token.Value.Bytes(), bitmap.GetCardinality(), len(freqs))
			}
			// Posting list and frequencies must name the same documents.
			it := bitmap.Iterator()
			for j := 0; it.HasNext(); j++ {
				if doc := it.Next(); doc != freqs[j].DocumentIndex {
					return corruptf("field %#x: token %q posting list and frequencies disagree at document %d", fieldHash, token.Value.Bytes(), doc)
				}
			}
		}
	}
	return nil
}
