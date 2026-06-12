package testsuite

import (
	"iter"
	"slices"
	"strings"
	"testing"

	"github.com/RogueTeam/textiplex/dorks"
	"github.com/RogueTeam/textiplex/query"
	"github.com/RogueTeam/textiplex/storage"
	"github.com/RogueTeam/textiplex/tokenizer"
	"github.com/RogueTeam/textiplex/tokenizer/en"
	"github.com/RogueTeam/textiplex/tokenizer/es"
	"github.com/stretchr/testify/assert"
)

func CompileQueryWith(t *testing.T, q string, def tokenizer.Tokenizer, fields map[uint64]tokenizer.Tokenizer) *query.SimpleQuery {
	t.Helper()
	assertions := assert.New(t)

	parsed, err := dorks.Parse(strings.NewReader(q))
	if !assertions.NoError(err, "parse %q", q) {
		return nil
	}

	sq := parsed.Compile(
		func(in []byte) (seq iter.Seq[*tokenizer.Token]) {
			return func(yield func(*tokenizer.Token) bool) {
				for entry := range def(in) {
					t.Logf("Entry Value: %s", entry.Value)
					if !yield(entry) {
						return
					}
				}
			}
		},
		fields,
	)
	if !assertions.NotNil(sq, "Compile(%q) returned nil — it must return the SimpleQuery it built", q) {
		return nil
	}

	return sq
}

func CompileEnglishQuery(t *testing.T, q string) *query.SimpleQuery {
	return CompileQueryWith(t, q, en.Tokenizer, nil)
}

func CompileSpanishQuery(t *testing.T, q string) *query.SimpleQuery {
	return CompileQueryWith(t, q, es.Tokenizer, nil)
}

func EnglishMatchedSet(t *testing.T, q string, s *storage.Storage) []string {
	return MatchedSetWith(t, q, s, en.Tokenizer, nil)
}

func SpanishMatchedSet(t *testing.T, q string, s *storage.Storage) []string {
	return MatchedSetWith(t, q, s, es.Tokenizer, nil)
}

func MatchedSetWith(t *testing.T, q string, s *storage.Storage, def tokenizer.Tokenizer, fields map[uint64]tokenizer.Tokenizer) []string {
	t.Helper()

	sq := CompileQueryWith(t, q, def, fields)
	if sq == nil {
		return nil
	}
	idxs, _ := RunQuery(sq, s)
	got := ResolveDocumentIndexes(s, idxs)
	slices.Sort(got)
	return got
}
