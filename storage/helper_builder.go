package storage

import (
	"github.com/RogueTeam/textiplex/tuple"
	"github.com/zeebo/xxh3"
)

// Construct helper types to improve query performance
func (s *Storage) BuildHelpers() {
	s.FieldDocLengths = make(map[uint64]uint64, len(s.Fields))
	s.FieldTokenDocFrequencies = make(map[uint64]uint64, len(s.Fields))

	var fieldsTokensDocsKey tuple.Tuple3[uint64]
	var fieldsDocsKey tuple.Tuple2[uint64]
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
