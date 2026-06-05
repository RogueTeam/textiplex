package storage

import "github.com/zeebo/xxh3"

type Tuple2[T any] struct {
	A, B T
}

type Tuple3[T any] struct {
	A, B, C T
}

// Construct helper types to improve query performance
func (s *Storage) BuildHelpers() {
	s.FieldDocLengths = make(map[Tuple2[uint64]]uint64, len(s.Fields))
	s.FieldTokenDocFrequencies = make(map[Tuple3[uint64]]uint64, len(s.Fields))

	for fieldHash, field := range s.Fields {
		for i := range field.DocumentLengths {
			docLength := &field.DocumentLengths[i]
			s.FieldDocLengths[Tuple2[uint64]{A: fieldHash, B: docLength.Index}] = docLength.Length
		}

		it := field.Tokens.Iter()
		for valid := it.First(); valid; valid = it.Next() {
			token := it.Item()
			tokHash := xxh3.Hash(token.Value)

			freqs := s.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

			for i := range freqs {
				s.FieldTokenDocFrequencies[Tuple3[uint64]{A: fieldHash, B: tokHash, C: freqs[i].DocumentIndex}] = freqs[i].Frequency
			}
		}
		it.Release()
	}

}
