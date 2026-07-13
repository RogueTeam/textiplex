package dorks

import (
	"io"
	"strings"

	"github.com/alecthomas/participle/v2"
	"github.com/alecthomas/participle/v2/lexer"
)

type UnquotedString string

func (v *UnquotedString) Capture(values []string) (err error) {
	value := values[0]
	if len(value) == 0 {
		return nil
	}

	if values[0][0] == '"' {
		*v = UnquotedString(strings.Clone(values[0][1 : len(values[0])-1]))
	} else {
		*v = UnquotedString(strings.Clone(values[0]))
	}
	return err
}

type Operator uint8

const (
	OperatorNone Operator = iota
	OperatorMust
	OperatorMustNot
)

func (o *Operator) Capture(values []string) (err error) {
	value := values[0]
	if len(value) == 0 {
		return nil
	}

	switch value[0] {
	case '+':
		*o = OperatorMust
	case '-':
		*o = OperatorMustNot
	}
	return nil
}

type MatchOperator uint8

const (
	MatchOperatorNone MatchOperator = iota
	MatchOperatorGreaterEqual
	MatchOperatorLessEqual
	MatchOperatorGreater
	MatchOperatorLess
)

func (o *MatchOperator) Capture(values []string) (err error) {
	value := values[0]
	if len(value) == 0 {
		return nil
	}

	switch value {
	case ">=":
		*o = MatchOperatorGreaterEqual
	case "<=":
		*o = MatchOperatorLessEqual
	case ">":
		*o = MatchOperatorGreater
	case "<":
		*o = MatchOperatorLess
	}
	return nil
}

type Match struct {
	Operator MatchOperator  `parser:"':' @MatchOperator?" json:"operator,omitzero"`
	Value    UnquotedString `parser:"@(Time | Float | Int | Keyword | Phrase)" json:"date,omitzero"`
}

type Dork struct {
	Operator Operator       `parser:"@( '+' | '-')?" json:"operator,omitzero"`
	Keyword  UnquotedString `parser:"@(Time | Float | Int | Phrase | Keyword | Phrase)" json:"keyword,omitzero"`
	Match    *Match         `parser:"@@?" json:"match,omitzero"`
	Boost    *float32       `parser:"(';' @(Float | Int))?" json:"boost,omitzero"`
	Fuzzy    *int           `parser:"('~' @Int)?" json:"fuzzy,omitzero"`
}

type Query struct {
	Dorks []*Dork `parser:"@@*"`
}

var parser = participle.MustBuild[Query](
	participle.Unquote("Phrase"),
	participle.Lexer(lexer.MustSimple([]lexer.SimpleRule{
		{Name: "whitespace", Pattern: `[ \t\n\r]+`},

		{Name: "Punctuation", Pattern: `:|;|~`},
		{Name: "MustOperator", Pattern: `\+|\-`},
		{Name: "MatchOperator", Pattern: "(<=)|(>=)|<|>"},
		{Name: "Time", Pattern: `(\d{4}-\d{2}-\d{2})|("\d{4}-\d{2}-\d{2}")`},
		{Name: "Float", Pattern: `(\d+\.\d+)|("\d+\.\d+")`},
		{Name: "Int", Pattern: `\d+|("\d+")`},
		{Name: "Phrase", Pattern: `"(\\"|[^"])*"`},
		{Name: "Keyword", Pattern: `[áéíóúñA-Za-z0-9]+[áéíóúñA-Za-z0-9!%"#$%&'()*+*,\-./<=>?@[\\\]^_` + "`" + `{|}]*`},
	})),
)

func Parse(r io.Reader) (q *Query, err error) {
	return parser.Parse(":memory:", r)
}
