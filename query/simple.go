package query

import (
	"bytes"
	"cmp"
	"slices"

	"github.com/RoaringBitmap/roaring/roaring64"
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

type Clause struct {
	Keywords      []*Keyword
	FieldKeywords map[uint64]*Keyword
	FieldRanges   map[uint64]*Range
}

func (c *Clause) Count() (count int) {
	return len(c.Keywords) + len(c.FieldKeywords) + len(c.FieldRanges)
}

func (c *Clause) Keyword(kw []byte, boost float64) {
	c.Keywords = append(c.Keywords, &Keyword{
		Value: kw,
		Boost: boost,
	})
}

func (c *Clause) FieldKeyword(field uint64, kw []byte, boost float64) {
	if c.FieldKeywords == nil {
		c.FieldKeywords = map[uint64]*Keyword{
			field: &Keyword{
				Value: kw,
				Boost: boost,
			},
		}
	} else {
		c.FieldKeywords[field] = &Keyword{
			Value: kw,
			Boost: boost,
		}
	}
}

func (c *Clause) FieldRange(field uint64, hi, lo []byte, boost float64) {
	if c.FieldRanges == nil {
		c.FieldRanges = map[uint64]*Range{
			field: &Range{
				High:  hi,
				Low:   lo,
				Boost: boost,
			},
		}
	} else {
		c.FieldRanges[field] = &Range{
			High:  hi,
			Low:   lo,
			Boost: boost,
		}
	}
}

type SimpleQuery struct {
	Shoulds  Clause
	Musts    Clause
	MustNots Clause
}

func NewSimpleQuery() *SimpleQuery {
	return &SimpleQuery{}
}

func (q *SimpleQuery) filterPassMusts(ctx *QueryContext, s *storage.Storage) {
	// Start with the full doc range and AND every Must condition down.
	// This avoids the firstAdded ordering dependency and is correct
	// regardless of map iteration order.
	ctx.Bitmap.AddRange(0, uint64(len(s.DocumentsIds)))

	var tokenGet storage.Token

	for _, kw := range q.Musts.Keywords {
		tokenGet.Value = kw.Value
		// Keyword on any field: union the matching posting lists across all
		// fields into a scratch bitmap, then AND that into ctx.Bitmap.
		var scratch roaring64.Bitmap
		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}

			scratch.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
		ctx.Bitmap.And(&scratch)
	}

	for fieldHash, kw := range q.Musts.FieldKeywords {
		field, found := s.Fields[fieldHash]
		if !found {
			// Field does not exist in index: no doc can satisfy this Must.
			ctx.Bitmap.Clear()
			return
		}
		tokenGet.Value = kw.Value
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			// Token absent in field: no doc can satisfy this Must.
			ctx.Bitmap.Clear()
			return
		}
		ctx.Bitmap.And(&s.PostingLists[tok.PostingListIndex].Bitmap)
	}

	for fieldHash, r := range q.Musts.FieldRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			ctx.Bitmap.Clear()
			return
		}
		var scratch roaring64.Bitmap
		tokenGet.Value = r.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, r.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, r.High) <= 0 {
			scratch.Or(&s.PostingLists[it.Item().PostingListIndex].Bitmap)
		}
		it.Release()
		ctx.Bitmap.And(&scratch)
	}
}

// This is the actual pass that will compute the scores needed for sorting
func (q *SimpleQuery) filterPassShoulds(ctx *QueryContext, s *storage.Storage) {
	var tokenGet storage.Token

	for _, kw := range q.Shoulds.Keywords {
		tokenGet.Value = kw.Value
		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}
			ctx.Bitmap.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
	}

	for fieldHash, kw := range q.Shoulds.FieldKeywords {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = kw.Value
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			continue
		}
		ctx.Bitmap.Or(&s.PostingLists[tok.PostingListIndex].Bitmap)
	}

	for fieldHash, r := range q.Shoulds.FieldRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = r.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, r.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, r.High) <= 0 {
			ctx.Bitmap.Or(&s.PostingLists[it.Item().PostingListIndex].Bitmap)
		}
		it.Release()
	}
}

func (q *SimpleQuery) filterPassMustNots(ctx *QueryContext, s *storage.Storage) {
	var tokenGet storage.Token

	for _, kw := range q.MustNots.Keywords {
		tokenGet.Value = kw.Value
		for _, field := range s.Fields {
			tok, found := field.Tokens.Get(&tokenGet)
			if !found {
				continue
			}
			ctx.Bitmap.AndNot(&s.PostingLists[tok.PostingListIndex].Bitmap)
		}
	}

	for fieldHash, kw := range q.MustNots.FieldKeywords {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = kw.Value
		tok, found := field.Tokens.Get(&tokenGet)
		if !found {
			continue
		}
		ctx.Bitmap.AndNot(&s.PostingLists[tok.PostingListIndex].Bitmap)
	}

	for fieldHash, r := range q.MustNots.FieldRanges {
		field, found := s.Fields[fieldHash]
		if !found {
			continue
		}
		tokenGet.Value = r.Low
		it := field.Tokens.Iter()
		for valid := it.Seek(&tokenGet) && bytes.Compare(it.Item().Value, r.High) <= 0; valid; valid = it.Next() && bytes.Compare(it.Item().Value, r.High) <= 0 {
			ctx.Bitmap.AndNot(&s.PostingLists[it.Item().PostingListIndex].Bitmap)
		}
		it.Release()
	}
}

// Query context intended to be cached and reused by caller on each search
type QueryContext struct {
	Bitmap roaring64.Bitmap
}

// Filter the documents id index into the destination bitmap
// the idea is to filter first the score results based on conditions
// is caller's responsability to clear dst bitmap
func (q *SimpleQuery) FilterDocuments(ctx *QueryContext, s *storage.Storage) {
	switch {
	case q.Musts.Count() > 0:
		q.filterPassMusts(ctx, s)
		q.filterPassShoulds(ctx, s)
		q.filterPassMustNots(ctx, s)
	case q.Shoulds.Count() > 0:
		q.filterPassShoulds(ctx, s)
		q.filterPassMustNots(ctx, s)
	case q.MustNots.Count() > 0:
		ctx.Bitmap.AddRange(0, uint64(len(s.DocumentsIds)))
		q.filterPassMustNots(ctx, s)
	}
}

// Once a filtering is done scoring is the next step of a searching algorithm
// Resolves the ctx to an actual idx slice
func (q *SimpleQuery) BM25(ctx *QueryContext) (idxs []uint64) {
	type bm25 struct {
		docIdx uint64
		score  float64
	}

	scores := make([]bm25, 0, ctx.Bitmap.GetCardinality())

	// TODO: Populate scores

	slices.SortFunc(scores, func(a, b bm25) int { return cmp.Compare(a.score, b.score) })

	idxs = make([]uint64, len(scores))
	for index := range scores {
		idxs[index] = scores[index].docIdx
	}
	return idxs
}
