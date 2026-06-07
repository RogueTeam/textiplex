package dorks

import (
	"io"
	"strconv"
	"time"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

type Time struct {
	Value time.Time
}

func (t *Time) Capture(values []string) (err error) {
	if values[0][0] == '"' {
		t.Value, err = time.Parse(time.DateOnly, values[0][1:len(values[0])-1])
	} else {
		t.Value, err = time.Parse(time.DateOnly, values[0])
	}
	return err
}

type Float struct {
	Value float64
}

func (f *Float) Capture(values []string) (err error) {
	if values[0][0] == '"' {
		f.Value, err = strconv.ParseFloat(values[0][1:len(values[0])-1], 64)
	} else {
		f.Value, err = strconv.ParseFloat(values[0], 64)
	}
	return err
}

type Integer struct {
	Value int64
}

func (i *Integer) Capture(values []string) (err error) {
	if values[0][0] == '"' {
		i.Value, err = strconv.ParseInt(values[0][1:len(values[0])-1], 10, 64)
	} else {
		i.Value, err = strconv.ParseInt(values[0], 10, 64)
	}
	return err
}

type Match struct {
	Operator string   `parser:"':' @MatchOperator?" json:"operator,omitzero"`
	Date     *Time    `parser:"(@Time" json:"date,omitzero"`
	Float    *Float   `parser:"| @Float" json:"float,omitzero"`
	Integer  *Integer `parser:"| @Int" json:"integer,omitzero"`
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
		{Name: "whitespace", Pattern: `\s+`},

		{Name: "Punctuation", Pattern: `:`},
		{Name: "MustOperator", Pattern: `\+|\-`},
		{Name: "MatchOperator", Pattern: "<|<=|>|>="},
		{Name: "Time", Pattern: `(\d{4}-\d{2}-\d{2})|("\d{4}-\d{2}-\d{2}")`},
		{Name: "Float", Pattern: `(\d+\.\d+)|("\d+\.\d+")`},
		{Name: "Int", Pattern: `\d+|("\d+")`},
		{Name: "Phrase", Pattern: `"(\\"|[^"])*"`},
		{Name: "Keyword", Pattern: `[^:]+`},
	})),
)

func Parse(r io.Reader) (q *Query, err error) {
	return parser.Parse(":memory:", r)
}
