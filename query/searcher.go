package query

import (
	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tuple"
	"github.com/zeebo/xxh3"
)

type DirectTokenFieldReference struct {
	Field     *storage.Field
	FieldHash uint64
	Token     *storage.Token
	TokenHash uint64
}

type Searcher struct {
	Storage *storage.Storage
	// Helpers, not intended to be saved in any matter but could be used to improve query performance
	// FieldDocLengths maps fieldHash -> document index -> length
	// Use Tuple2.Hash for generating the keys
	// A: Field Hash
	// B: Document Index
	FieldDocLengths map[uint64]uint64
	// FieldTokenDocFrequencies field hash -> token hash -> document index -> frequency
	// Use Tuple3.Hash for generating the keys
	// A: Field Hash
	// B: Token Hash
	// C: Document Index
	FieldTokenDocFrequencies map[uint64]uint64
	// Faster looks for fields containing a specific token
	// Keys are the hashes of the tokens
	TokenFields map[uint64][]*DirectTokenFieldReference
	// Faster looks for tokens mapping fields
	// Keys are the hashes of the tokens
	// Keys are constructed using Tuple2.Hash
	// A: Field Hash
	// B: Token Hash
	FieldTokens map[uint64]*DirectTokenFieldReference
}

// Construct helper types to improve query performance
func (s *Searcher) BuildHelpers() {
	s.FieldDocLengths = make(map[uint64]uint64, len(s.Storage.Fields))
	s.FieldTokenDocFrequencies = make(map[uint64]uint64, len(s.Storage.Fields))
	s.TokenFields = make(map[uint64][]*DirectTokenFieldReference)
	s.FieldTokens = make(map[uint64]*DirectTokenFieldReference)

	tokenFieldsPool := pool.New[DirectTokenFieldReference](20)

	var fieldsTokensDocsKey tuple.Tuple3[uint64]
	var fieldsDocsKey tuple.Tuple2[uint64]
	for fieldHash, field := range s.Storage.Fields {
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
			tokenHash := xxh3.Hash(token.Value)

			fieldsTokensDocsKey.B = tokenHash

			freqs := s.Storage.TokenFrequencies[token.FrequenciesIndex : token.FrequenciesIndex+token.FrequencyCount]

			for i := range freqs {
				fieldsTokensDocsKey.C = freqs[i].DocumentIndex
				s.FieldTokenDocFrequencies[fieldsTokensDocsKey.Hash()] = freqs[i].Frequency
			}

			tokenFieldReference := tokenFieldsPool.Get()
			*tokenFieldReference = DirectTokenFieldReference{
				Field:     field,
				FieldHash: fieldHash,
				Token:     token,
				TokenHash: tokenHash,
			}
			s.TokenFields[tokenHash] = append(s.TokenFields[tokenHash], tokenFieldReference)
			s.FieldTokens[fieldsTokensDocsKey.Hash2()] = tokenFieldReference
		}
		it.Release()
	}
}

func New(s *storage.Storage) (searcher *Searcher) {
	searcher = &Searcher{
		Storage: s,
	}
	searcher.BuildHelpers()
	return searcher
}
