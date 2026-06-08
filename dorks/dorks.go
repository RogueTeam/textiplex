package dorks

import (
	"io"
	"strconv"
	"strings"
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

type Keyword string

func (k *Keyword) Capture(values []string) (err error) {
	value := values[0]
	if len(value) == 0 {
		return nil
	}
	if value[0] == '"' {
		*k = Keyword(strings.Clone(values[0][1 : len(values[0])-1]))
	} else {
		*k = Keyword(strings.Clone(values[0]))
	}
	return nil
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
	Operator MatchOperator `parser:"':' @MatchOperator?" json:"operator,omitzero"`
	Date     *Time         `parser:"(@Time" json:"date,omitzero"`
	Float    *Float        `parser:"| @Float" json:"float,omitzero"`
	Integer  *Integer      `parser:"| @Int" json:"integer,omitzero"`
	Keyword  *string       `parser:"| @(Keyword | Phrase))" json:"keyword,omitzero"`
}

type Dork struct {
	Operator Operator `parser:"@( '+' | '-')?" json:"operator,omitzero"`
	Keyword  Keyword  `parser:"@(Time | Float | Int | Phrase | Keyword)" json:"keyword,omitzero"`
	Match    *Match   `parser:"@@?" json:"match,omitzero"`
	Boost    *float64 `parser:"(';' @(Float | Int))?" json:"boost,omitzero"`
	Fuzzy    *uint8   `parser:"('~' @Int)?" json:"fuzzy,omitzero"`
}

type Query struct {
	Dorks []*Dork `parser:"@@*"`
}

var parser = participle.MustBuild[Query](
	participle.Unquote("Phrase"),
	participle.Lexer(lexer.MustSimple([]lexer.SimpleRule{
		{Name: "whitespace", Pattern: `[ \t\n\r]+`},

		{Name: "Punctuation", Pattern: `:|;`},
		{Name: "MustOperator", Pattern: `\+|\-`},
		{Name: "MatchOperator", Pattern: "(<=)|(>=)|<|>"},
		{Name: "Time", Pattern: `(\d{4}-\d{2}-\d{2})|("\d{4}-\d{2}-\d{2}")`},
		{Name: "Float", Pattern: `(\d+\.\d+)|("\d+\.\d+")`},
		{Name: "Int", Pattern: `\d+|("\d+")`},
		{Name: "Phrase", Pattern: `"(\\"|[^"])*"`},
		{Name: "Keyword", Pattern: `[áéíóúñA-Za-z0-9]+[áéíóúñA-Za-z0-9!%"#$%&'()*+*,\-./<=>?@[\\\]^_` + "`" + `{|}~]*`},
	})),
)

func Parse(r io.Reader) (q *Query, err error) {
	return parser.Parse(":memory:", r)
}
