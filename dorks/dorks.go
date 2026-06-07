package dorks

import (
	"io"
	"time"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

type Time struct {
	Value time.Time
}

func (t *Time) Capture(values []string) (err error) {
	t.Value, err = time.Parse(time.DateOnly, values[0])
	return err
}

type Match struct {
	Operator string   `parser:"':' @MatchOperator?" json:"operator,omitzero"`
	Date     *Time    `parser:"(@Time" json:"date,omitzero"`
	Float    *float64 `parser:"| @Float" json:"float,omitzero"`
	Integer  *int64   `parser:"| @Int" json:"integer,omitzero"`
	Keyword  *string  `parser:"| @(Keyword | Phrase))" json:"keyword,omitzero"`
}

type Dork struct {
	Operator string `parser:"@( '+' | '-')?" json:"operator,omitzero"`
	Keyword  string `parser:"@(Int | Float | Phrase | Keyword)" json:"keyword,omitzero"`
	Match    *Match `parser:"@@?" json:"match,omitzero"`
}

type Query struct {
	Dorks []*Dork `parser:"@@*"`
}

var parser = participle.MustBuild[Query](
	participle.Unquote("Phrase"),
	participle.Lexer(lexer.MustSimple([]lexer.SimpleRule{
		{Name: "whitespace", Pattern: `[ \t]+`},
		{Name: "EOL", Pattern: `[\n\r]+`},

		{Name: "Punctuation", Pattern: `:`},
		{Name: "MustOperator", Pattern: `\+|\-`},
		{Name: "MatchOperator", Pattern: "<|<=|>|>="},
		{Name: "Time", Pattern: `\d{4}-\d{2}-\d{2}`},
		{Name: "Float", Pattern: `\d+\.\d+`},
		{Name: "Int", Pattern: `\d+`},
		{Name: "Phrase", Pattern: `"(\\"|[^"])*"`},
		{Name: "Keyword", Pattern: `[^:]+`},
	})),
)

func Parse(r io.Reader) (q *Query, err error) {
	return parser.Parse(":memory:", r)
}
