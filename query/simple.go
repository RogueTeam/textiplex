package query

import (
	"bytes"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
)

type Range struct {
	Low, High []byte
}

type SimpleQuery struct {
	rangePool         *pool.Pool[Range]
	Shoulds           [][]byte
	ShouldFields      map[uint64][]byte
	ShouldFieldRanges map[uint64]*Range

	Musts      [][]byte
	MustFields map[uint64][]byte
	MustRanges map[uint64]*Range

	MustNots           [][]byte
	MustNotFields      map[uint64][]byte
	MustNotFieldRanges map[uint64]*Range
}

// Keyword could be present on any field
func (q *SimpleQuery) Should(keyword []byte) (o *SimpleQuery) {
	q.Shoulds = append(q.Shoulds, keyword)
	return q
}

// Keyword should be present on the field
func (q *SimpleQuery) ShouldField(field uint64, keyword []byte) (o *SimpleQuery) {
	if q.ShouldFields == nil {
		q.ShouldFields = map[uint64][]byte{
			field: keyword,
		}
	} else {
		q.ShouldFields[field] = keyword
	}
	return q
}

// Range could be present on the field
func (q *SimpleQuery) ShouldFieldRange(field uint64, low, high []byte) (o *SimpleQuery) {
	lrRange := q.rangePool.Get()
	lrRange.Low = low
	lrRange.High = high

	if q.ShouldFieldRanges == nil {
		q.ShouldFieldRanges = map[uint64]*Range{
			field: lrRange,
		}
	} else {
		q.ShouldFieldRanges[field] = lrRange
	}
	return q
}

// Keyword must be present on any field
func (q *SimpleQuery) Must(keyword []byte) (o *SimpleQuery) {
	q.Musts = append(q.Musts, keyword)
	return q
}

// Keyword must be present on the field
func (q *SimpleQuery) MustField(field uint64, keyword []byte) (o *SimpleQuery) {
	if q.MustFields == nil {
		q.MustFields = map[uint64][]byte{
			field: keyword,
		}
	} else {
		q.MustFields[field] = keyword
	}
	return q
}

// Range must be present on the field
func (q *SimpleQuery) MustFieldRange(field uint64, low, high []byte) (o *SimpleQuery) {
	lrRange := q.rangePool.Get()
	lrRange.Low = low
	lrRange.High = high

	if q.MustRanges == nil {
		q.MustRanges = map[uint64]*Range{
			field: lrRange,
		}
	} else {
		q.MustRanges[field] = lrRange
	}
	return q
}

// Keyword must not be present on any field
func (q *SimpleQuery) MustNot(keyword []byte) (o *SimpleQuery) {
	q.MustNots = append(q.MustNots, keyword)
	return q
}

// Keyword must not be present on the field
func (q *SimpleQuery) MustNotField(field uint64, keyword []byte) (o *SimpleQuery) {
	if q.MustNotFields == nil {
		q.MustNotFields = map[uint64][]byte{
			field: keyword,
		}
	} else {
		q.MustNotFields[field] = keyword
	}
	return q
}

// Range must not be present on the field
func (q *SimpleQuery) MustNotFieldRange(field uint64, low, high []byte) (o *SimpleQuery) {
	lrRange := q.rangePool.Get()
	lrRange.Low = low
	lrRange.High = high

	if q.MustNotFieldRanges == nil {
		q.MustNotFieldRanges = map[uint64]*Range{
			field: lrRange,
		}
	} else {
		q.MustNotFieldRanges[field] = lrRange
	}
	return q
}

func (q *SimpleQuery) filterDocumentsWithMust(dst *roaring64.Bitmap, s *storage.Storage) {
	var tokenGet storage.Token
	var firstAdded bool

	for _, keyword := range q.Musts {
		tokenGet.Value = keyword

		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}

			if !firstAdded {
				dst.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
				firstAdded = true
			} else {
				dst.And(&s.PostingLists[tok.PostingListIndex].Bitmap)
			}
		}
	}

	for fieldHash, keyword := range q.MustFields {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}

		tokenGet.Value = keyword
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			continue
		}

		if !firstAdded {
			dst.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
			firstAdded = true
		} else {
			dst.And(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
	}

	for fieldHash, lrRange := range q.MustRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}

		tokenGet.Value = lrRange.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, lrRange.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, lrRange.High) <= 0 {
			tok := it.Item()

			if !firstAdded {
				dst.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
				firstAdded = true
			} else {
				dst.And(&s.PostingLists[tok.PostingListIndex].Bitmap)
			}
		}
		it.Release()
	}
}

func (q *SimpleQuery) filterDocumentsWithShould(dst *roaring64.Bitmap, s *storage.Storage) {
	var tokenGet storage.Token
	for _, keyword := range q.Shoulds {
		tokenGet.Value = keyword

		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}

			dst.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
	}

	for fieldHash, keyword := range q.ShouldFields {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}

		tokenGet.Value = keyword
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			continue
		}

		dst.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
	}

	for fieldHash, lrRange := range q.ShouldFieldRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}

		tokenGet.Value = lrRange.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, lrRange.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, lrRange.High) <= 0 {
			tok := it.Item()

			dst.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
		it.Release()
	}
}

func (q *SimpleQuery) applyMustNots(dst *roaring64.Bitmap, s *storage.Storage) {
	var tokenGet storage.Token

	for _, keyword := range q.MustNots {
		tokenGet.Value = keyword
		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}
			dst.AndNot(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
	}

	for fieldHash, keyword := range q.MustNotFields {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = keyword
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			continue
		}
		dst.AndNot(&s.PostingLists[tok.PostingListIndex].Bitmap)
	}

	for fieldHash, lrRange := range q.MustNotFieldRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = lrRange.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, lrRange.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, lrRange.High) <= 0 {
			dst.AndNot(&s.PostingLists[it.Item().PostingListIndex].Bitmap)
		}
		it.Release()
	}
}

// Filters the documents based on the conditions provided
// dst is the dst roaring bitmap which will contain all the indexes of the documents ids
// It is used as a cached target to prevent unnecessary allocations of underlying buffer
// Returned array corresponds to the indexes of all computed indexes sorted by the BM25 score or field order
func (q *SimpleQuery) FilterDocuments(dst *roaring64.Bitmap, s *storage.Storage) (idxs []uint64) {
	dst.Clear()

	switch {
	case len(q.Musts) > 0 || len(q.MustRanges) > 0 || len(q.MustFields) > 0:
		q.filterDocumentsWithMust(dst, s)
		q.applyMustNots(dst, s)
	case len(q.Shoulds) > 0 || len(q.ShouldFieldRanges) > 0 || len(q.ShouldFields) > 0:
		q.filterDocumentsWithShould(dst, s)
		q.applyMustNots(dst, s)
	case len(q.MustNots) > 0 || len(q.MustNotFieldRanges) > 0 || len(q.MustNotFields) > 0:
		// Must not is an inverse query, if nothing what provided
		// we start with the full range'
		// Then we remove those we don't want
		dst.AddRange(0, uint64(len(s.DocumentsIds)))
		q.applyMustNots(dst, s)
	}

	// No values provided we can return inmediatly
	if dst.IsEmpty() {
		return nil
	}

	idxs = dst.ToArray()
	// TODO: Populate idxs
	// Now, for each index if the sorting mode was
	return idxs
}

func NewSimpleQuery(rangePoolSize int) (q *SimpleQuery) {
	return &SimpleQuery{
		rangePool: pool.New[Range](rangePoolSize),
	}
}
