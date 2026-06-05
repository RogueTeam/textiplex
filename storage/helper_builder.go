package storage

import (
	"unsafe"

	"github.com/zeebo/xxh3"
)

type Tuple2[T any] struct {
	A, B T
}

func (t *Tuple2[T]) Hash() (hash uint64) {
	return xxh3.Hash(unsafe.Slice((*byte)(unsafe.Pointer(t)), unsafe.Sizeof(Tuple2[T]{})))
}

type Tuple3[T any] struct {
	A, B, C T
}

func (t *Tuple3[T]) Hash() (hash uint64) {
	return xxh3.Hash(unsafe.Slice((*byte)(unsafe.Pointer(t)), unsafe.Sizeof(Tuple3[T]{})))
}

// Construct helper types to improve query performance
func (s *Storage) BuildHelpers() {
	s.FieldDocLengths = make(map[uint64]uint64, len(s.Fields))
	s.FieldTokenDocFrequencies = make(map[uint64]uint64, len(s.Fields))

	var fieldsTokensDocsKey Tuple3[uint64]
	var fieldsDocsKey Tuple2[uint64]
	for fieldHash, field := range s.Fields {
		fieldsTokensDocsKey.A = fieldHash
		fieldsDocsKey.A = fieldHash
		for i := range field.DocumentLengths {
			docLength := &field.DocumentLengths[i]
			fieldsDocsKey.B = docLength.Index
			s.FieldDocLengths[fieldsDocsKey.Hash()] = docLength.Length
		}

		it := field.Tokens.Iter()
		for valid := it.First(); valid; valid = it.Next() {
			token := it.Item()
			fieldsTokensDocsKey.B = xxh3.Hash(token.Value)

			freqs := s.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

			for i := range freqs {
				fieldsTokensDocsKey.C = freqs[i].DocumentIndex
				s.FieldTokenDocFrequencies[fieldsTokensDocsKey.Hash()] = freqs[i].Frequency
			}
		}
		it.Release()
	}

}
