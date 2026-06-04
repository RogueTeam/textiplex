package query

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/RoaringBitmap/roaring/roaring64"
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
)

type Range struct {
	Boost     float64
	Low, High []byte
}

type Keyword struct {
	Boost float64
	Value []byte
}

type SimpleQuery struct {
	rangePool          *pool.Pool[Range]
	kwPool             *pool.Pool[Keyword]
	Shoulds            []*Keyword
	ShouldFields       map[uint64]*Keyword
	ShouldFieldRanges  map[uint64]*Range
	Musts              []*Keyword
	MustFields         map[uint64]*Keyword
	MustRanges         map[uint64]*Range
	MustNots           []*Keyword
	MustNotFields      map[uint64]*Keyword
	MustNotFieldRanges map[uint64]*Range
}

func NewSimpleQuery() *SimpleQuery {
	return &SimpleQuery{
		rangePool: pool.New[Range](4),
		kwPool:    pool.New[Keyword](8),
	}
}

// Should: keyword may be present on any field, contributes to score
func (q *SimpleQuery) Should(keyword []byte, boost float64) *SimpleQuery {
	kw := q.kwPool.Get()
	kw.Value = keyword
	kw.Boost = boost
	q.Shoulds = append(q.Shoulds, kw)
	return q
}

// ShouldField: keyword may be present on the specific field
func (q *SimpleQuery) ShouldField(field uint64, keyword []byte, boost float64) *SimpleQuery {
	kw := q.kwPool.Get()
	kw.Value = keyword
	kw.Boost = boost
	if q.ShouldFields == nil {
		q.ShouldFields = make(map[uint64]*Keyword)
	}
	q.ShouldFields[field] = kw
	return q
}

// ShouldFieldRange: range may be present on the specific field
func (q *SimpleQuery) ShouldFieldRange(field uint64, low, high []byte, boost float64) *SimpleQuery {
	r := q.rangePool.Get()
	r.Low = low
	r.High = high
	r.Boost = boost
	if q.ShouldFieldRanges == nil {
		q.ShouldFieldRanges = make(map[uint64]*Range)
	}
	q.ShouldFieldRanges[field] = r
	return q
}

// Must: keyword must be present on any field
func (q *SimpleQuery) Must(keyword []byte, boost float64) *SimpleQuery {
	kw := q.kwPool.Get()
	kw.Value = keyword
	kw.Boost = boost
	q.Musts = append(q.Musts, kw)
	return q
}

// MustField: keyword must be present on the specific field
func (q *SimpleQuery) MustField(field uint64, keyword []byte, boost float64) *SimpleQuery {
	kw := q.kwPool.Get()
	kw.Value = keyword
	kw.Boost = boost
	if q.MustFields == nil {
		q.MustFields = make(map[uint64]*Keyword)
	}
	q.MustFields[field] = kw
	return q
}

// MustFieldRange: range must be present on the specific field
func (q *SimpleQuery) MustFieldRange(field uint64, low, high []byte, boost float64) *SimpleQuery {
	r := q.rangePool.Get()
	r.Low = low
	r.High = high
	r.Boost = boost
	if q.MustRanges == nil {
		q.MustRanges = make(map[uint64]*Range)
	}
	q.MustRanges[field] = r
	return q
}

// MustNot: keyword must not be present on any field
func (q *SimpleQuery) MustNot(keyword []byte) *SimpleQuery {
	kw := q.kwPool.Get()
	kw.Value = keyword
	kw.Boost = 0
	q.MustNots = append(q.MustNots, kw)
	return q
}

// MustNotField: keyword must not be present on the specific field
func (q *SimpleQuery) MustNotField(field uint64, keyword []byte) *SimpleQuery {
	kw := q.kwPool.Get()
	kw.Value = keyword
	kw.Boost = 0
	if q.MustNotFields == nil {
		q.MustNotFields = make(map[uint64]*Keyword)
	}
	q.MustNotFields[field] = kw
	return q
}

// MustNotFieldRange: range must not be present on the specific field
func (q *SimpleQuery) MustNotFieldRange(field uint64, low, high []byte) *SimpleQuery {
	r := q.rangePool.Get()
	r.Low = low
	r.High = high
	r.Boost = 0
	if q.MustNotFieldRanges == nil {
		q.MustNotFieldRanges = make(map[uint64]*Range)
	}
	q.MustNotFieldRanges[field] = r
	return q
}

func (q *SimpleQuery) filterDocumentsWithMust(dst *roaring64.Bitmap, s *storage.Storage) {
	// Start with the full doc range and AND every Must condition down.
	// This avoids the firstAdded ordering dependency and is correct
	// regardless of map iteration order.
	dst.AddRange(0, uint64(len(s.DocumentsIds)))

	var tokenGet storage.Token

	for _, kw := range q.Musts {
		tokenGet.Value = kw.Value
		// Keyword on any field: union the matching posting lists across all
		// fields into a scratch bitmap, then AND that into dst.
		var scratch roaring64.Bitmap
		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}
			scratch.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
		dst.And(&scratch)
	}

	for fieldHash, kw := range q.MustFields {
		field, found := s.Fields[fieldHash]
		if !found {
			// Field does not exist in index: no doc can satisfy this Must.
			dst.Clear()
			return
		}
		tokenGet.Value = kw.Value
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			// Token absent in field: no doc can satisfy this Must.
			dst.Clear()
			return
		}
		dst.And(&s.PostingLists[tok.PostingListIndex].Bitmap)
	}

	for fieldHash, r := range q.MustRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			dst.Clear()
			return
		}
		var scratch roaring64.Bitmap
		tokenGet.Value = r.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, r.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, r.High) <= 0 {
			scratch.Or(&s.PostingLists[it.Item().PostingListIndex].Bitmap)
		}
		it.Release()
		dst.And(&scratch)
	}
}

func (q *SimpleQuery) filterDocumentsWithShould(dst *roaring64.Bitmap, s *storage.Storage) {
	var tokenGet storage.Token

	for _, kw := range q.Shoulds {
		tokenGet.Value = kw.Value
		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}
			dst.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
	}

	for fieldHash, kw := range q.ShouldFields {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = kw.Value
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			continue
		}
		dst.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
	}

	for fieldHash, r := range q.ShouldFieldRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = r.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, r.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, r.High) <= 0 {
			dst.Or(&s.PostingLists[it.Item().PostingListIndex].Bitmap)
		}
		it.Release()
	}
}

func (q *SimpleQuery) applyMustNots(dst *roaring64.Bitmap, s *storage.Storage) {
	var tokenGet storage.Token

	for _, kw := range q.MustNots {
		tokenGet.Value = kw.Value
		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}
			dst.AndNot(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
	}

	for fieldHash, kw := range q.MustNotFields {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = kw.Value
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			continue
		}
		dst.AndNot(&s.PostingLists[tok.PostingListIndex].Bitmap)
	}

	for fieldHash, r := range q.MustNotFieldRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = r.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, r.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, r.High) <= 0 {
			dst.AndNot(&s.PostingLists[it.Item().PostingListIndex].Bitmap)
		}
		it.Release()
	}
}

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (q *SimpleQuery) FilterDocuments(dst *roaring64.Bitmap, s *storage.Storage) {
	switch {
	case len(q.Musts) > 0 || len(q.MustRanges) > 0 || len(q.MustFields) > 0:
		q.filterDocumentsWithMust(dst, s)
		q.applyMustNots(dst, s)
	case len(q.Shoulds) > 0 || len(q.ShouldFieldRanges) > 0 || len(q.ShouldFields) > 0:
		q.filterDocumentsWithShould(dst, s)
		q.applyMustNots(dst, s)
	case len(q.MustNots) > 0 || len(q.MustNotFieldRanges) > 0 || len(q.MustNotFields) > 0:
		dst.AddRange(0, uint64(len(s.DocumentsIds)))
		q.applyMustNots(dst, s)
	}
}

// Once a filtering is done scoring is the next step of a searching algorithm
func (q *SimpleQuery) BM25(src *roaring64.Bitmap) (idxs []uint64) {
	type bm25 struct {
		docIdx uint64
		score  float64
	}

	scores := make([]bm25, 0, src.GetCardinality())

	// TODO: Populate scores

	slices.SortFunc(scores, func(a, b bm25) int { return cmp.Compare(a.score, b.score) })

	idxs = make([]uint64, len(scores))
	for index := range scores {
		idxs[index] = scores[index].docIdx
	}
	return idxs
}
