package query_test

import (
	"encoding/json"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/RogueTeam/textiplex"
	"github.com/RogueTeam/textiplex/dorks"
	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/date"
	"github.com/RogueTeam/textiplex/tokenizer/en"
	"github.com/RogueTeam/textiplex/tokenizer/es"
	"github.com/RogueTeam/textiplex/tokenizer/floating"
	"github.com/RogueTeam/textiplex/tokenizer/integer"
	"github.com/RogueTeam/textiplex/tokenizer/keyword"
	"github.com/stretchr/testify/assert"
	"github.com/zeebo/xxh3"
)

const (
	CustomBm25IndexBenchEnv = "CUSTOM_BM25_INDEX"
	IndexPathEnv            = "INDEX_PATH"
	QueryStringEnv          = "QUERY_STRING"
	AllFieldEnv             = "ALLFIELD_NAME"
	DefaultTokenizerEnv     = "DEFAULT_TOKENIZER"
	FieldTokenizersEnv      = "FIELD_TOKENIZERS"
)

var (
	CustomBm25IndexBench   = os.Getenv(CustomBm25IndexBenchEnv) != ""
	IndexPath              = os.Getenv(IndexPathEnv)
	QueryString            = os.Getenv(QueryStringEnv)
	AllField               textiplex.SortField
	DefaultTokenizerString = strings.ToLower(strings.TrimSpace(os.Getenv(DefaultTokenizerEnv)))
	FieldTokenizersString  = os.Getenv(FieldTokenizersEnv)
)

var (
	DefaultTokenizer tokenizer.Tokenizer
	FieldTokenizers  map[uint64]tokenizer.Tokenizer
)

func init() {
	if !CustomBm25IndexBench {
		return
	}

	switch DefaultTokenizerString {
	case "en":
		DefaultTokenizer = en.TokenizerWithoutStopwords
	case "es":
		DefaultTokenizer = es.TokenizerWithoutStopwords
	default:
		log.Fatalf("%s should be set to en or es", DefaultTokenizerEnv)
	}

	allFieldStr := os.Getenv(AllFieldEnv)
	if allFieldStr != "" {
		AllField = textiplex.SortField(xxh3.HashString(allFieldStr))
	}

	var fieldTokenizers map[string]string
	err := json.Unmarshal([]byte(FieldTokenizersString), &fieldTokenizers)
	if err != nil {
		log.Fatalf("Failed to parse field tokenizers: %v", err)
	}

	FieldTokenizers = make(map[uint64]tokenizer.Tokenizer, len(fieldTokenizers))
	for field, tokenizerString := range fieldTokenizers {
		tokenizerString = strings.ToLower(strings.TrimSpace(tokenizerString))

		var tokenizerFunc tokenizer.Tokenizer
		switch tokenizerString {
		case "en":
			tokenizerFunc = en.TokenizerWithoutStopwords
		case "es":
			tokenizerFunc = es.TokenizerWithoutStopwords
		case "keyword":
			tokenizerFunc = keyword.Tokenizer
		case "float":
			tokenizerFunc = floating.Tokenizer
		case "int":
			tokenizerFunc = integer.Tokenizer
		case "date":
			tokenizerFunc = date.Tokenizer
		default:
			log.Fatalf("%s: field %s contains an invalid tokenizer func: %s, it only be set to keyword, float, int, date, en or es", FieldTokenizersEnv, field, tokenizerString)
		}

		FieldTokenizers[xxh3.HashString(field)] = tokenizerFunc
	}
}

func BenchmarkCustomIndexSearchShould(b *testing.B) {
	if !CustomBm25IndexBench {
		b.Skip()
		return
	}

	assertions := assert.New(b)

	var s storage.Storage
	err := s.Load(IndexPath)
	if !assertions.NoError(err, "failed to load index from path") {
		return
	}

	q, err := dorks.Parse(strings.NewReader(QueryString))
	if !assertions.NoError(err, "failed to parse query string") {
		return
	}

	sq := q.Compile(uint64(AllField), DefaultTokenizer, FieldTokenizers)

	var searcher = query.New(&s)

	b.ReportAllocs()

	var logged bool
	for b.Loop() {
		var ctx query.QueryContext
		searcher.FilterDocuments(&ctx, sq)
		searcher.BM25Score(&ctx, sq)
		searcher.ResolveScores(&ctx)

		b.SetBytes(int64(ctx.Scoring.Len()) * 4)
		if !logged {
			logged = true
			b.Logf("Found #: %d", ctx.Scoring.Len())
		}
	}
}
