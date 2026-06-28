package dorks_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RogueTeam/textiplex/dorks"
	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	t.Run("Failure", func(t *testing.T) {
		type Test struct {
			Query string
		}
		tests := []Test{
			{Query: ":value"},
			{Query: "field:"},
			{Query: "field::value"},
			{Query: "price:>-10"},
			{Query: "field:<"},
		}
		for _, test := range tests {
			t.Run(test.Query, func(t *testing.T) {
				assertions := assert.New(t)

				_, err := dorks.Parse(strings.NewReader(test.Query))
				assertions.Error(err, "expected a parse error for %q", test.Query)
			})
		}
	})
	t.Run("Success", func(t *testing.T) {
		type Test struct {
			Query  string
			Expect dorks.Query
		}
		tests := []Test{
			{Query: "Hello", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Hello"}}}},
			{Query: "+Hello", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "Hello"}}}},
			{Query: "-Hello", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "Hello"}}}},
			{Query: "-1000", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "1000"}}}},
			{Query: `"Hello Antonio"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Hello Antonio"}}}},
			{Query: `-"Hello Paula"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "Hello Paula"}}}},
			{Query: "created_at:2020-01-31", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "created_at", Match: &dorks.Match{Value: "2020-01-31"}}}}},
			{Query: `created_at:"2020-01-31"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "created_at", Match: &dorks.Match{Value: "2020-01-31"}}}}},
			{Query: "price_cop:10", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Value: "10"}}}}},
			{Query: "price_usd:0.99", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_usd", Match: &dorks.Match{Value: "0.99"}}}}},
			{Query: `price_cop:"10"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Value: "10"}}}}},
			{Query: `price_usd:"0.99"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_usd", Match: &dorks.Match{Value: "0.99"}}}}},
			{Query: "price_cop:<10", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Operator: dorks.MatchOperatorLess, Value: "10"}}}}},
			{Query: "price_usd:<=0.99", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_usd", Match: &dorks.Match{Operator: dorks.MatchOperatorLessEqual, Value: "0.99"}}}}},
			{Query: "price_cop:>10", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Operator: dorks.MatchOperatorGreater, Value: "10"}}}}},
			{Query: "price_usd:>=0.99", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_usd", Match: &dorks.Match{Operator: dorks.MatchOperatorGreaterEqual, Value: "0.99"}}}}},
			{Query: `full-name:"Antonio Donis"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "full-name", Match: &dorks.Match{Value: "Antonio Donis"}}}}},
			{Query: `full-name:>"Antonio"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "full-name", Match: &dorks.Match{Operator: dorks.MatchOperatorGreater, Value: "Antonio"}}}}},
			{Query: "Hello", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Hello"}}}},
			{Query: "world", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "world"}}}},
			{Query: "a", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "a"}}}},
			{Query: "Z", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Z"}}}},
			{Query: "MixedCase123", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "MixedCase123"}}}},
			{Query: "café", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "café"}}}},
			{Query: "user_name", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "user_name"}}}},
			{Query: "snake_case_value", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "snake_case_value"}}}},
			{Query: "kebab-name", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "kebab-name"}}}},
			{Query: "with.dot", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "with.dot"}}}},
			{Query: "v1.2.3", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "v1.2.3"}}}},
			{Query: "trailing-dash-", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "trailing-dash-"}}}},
			{Query: "value!", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "value!"}}}},
			{Query: "C++", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "C++"}}}},
			{Query: "a&b", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "a&b"}}}},
			{Query: "x=y", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "x=y"}}}},
			{Query: "path/to/file", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "path/to/file"}}}},
			{Query: "email@host", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "email@host"}}}},
			{Query: "Hello", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Hello"}}}},
			{Query: "multi word phrase here", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "multi"}, {Keyword: "word"}, {Keyword: "phrase"}, {Keyword: "here"}}}},
			{Query: "a+b", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "a+b"}}}},
			{Query: "a<b", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "a<b"}}}},
			{Query: "discount%", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "discount%"}}}},
			{Query: "tag#", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "tag#"}}}},
			{Query: "wild*", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "wild*"}}}},
			{Query: "+Hello", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "Hello"}}}},
			{Query: "-Hello", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "Hello"}}}},
			{Query: "+world", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "world"}}}},
			{Query: "-snake_case_value", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "snake_case_value"}}}},
			{Query: "+café", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "café"}}}},
			{Query: "-kebab-name", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "kebab-name"}}}},
			{Query: "+a", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "a"}}}},
			{Query: "-a", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "a"}}}},
			{Query: "+Hello World", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "Hello"}, {Keyword: "World"}}}},
			{Query: "42", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "42"}}}},
			{Query: "007", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "007"}}}},
			{Query: "1000000", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "1000000"}}}},
			{Query: "3.14", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "3.14"}}}},
			{Query: "0.0", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "0.0"}}}},
			{Query: "123.456", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "123.456"}}}},
			{Query: "+100", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "100"}}}},
			{Query: "-1000", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "1000"}}}},
			{Query: "+0.5", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "0.5"}}}},
			{Query: "-3.14", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "3.14"}}}},
			{Query: "1.5x", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "1.5"}, {Keyword: "x"}}}},
			{Query: "42abc", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "42"}, {Keyword: "abc"}}}},
			{Query: `"a""b"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "a"}, {Keyword: "b"}}}},
			{Query: `"first"second`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "first"}, {Keyword: "second"}}}},
			{Query: `"x"42`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "x"}, {Keyword: "42"}}}},
			{Query: `+"a"-"b"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "a"}, {Operator: dorks.OperatorMustNot, Keyword: "b"}}}},
			{Query: "price_cop:1000000 status:active", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Value: "1000000"}}, {Keyword: "status", Match: &dorks.Match{Value: "active"}}}}},
			{Query: "created_at:2020-01-31 country:Colombia", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "created_at", Match: &dorks.Match{Value: "2020-01-31"}}, {Keyword: "country", Match: &dorks.Match{Value: "Colombia"}}}}},
			{Query: `"Antonio Donis" city:Bogota`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Antonio Donis"}, {Keyword: "city", Match: &dorks.Match{Value: "Bogota"}}}}},
			{Query: "age:25 qty:0", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "age", Match: &dorks.Match{Value: "25"}}, {Keyword: "qty", Match: &dorks.Match{Value: "0"}}}}},
			{Query: "42 1000", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "42"}, {Keyword: "1000"}}}},
			{Query: "qty:0 active", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "qty", Match: &dorks.Match{Value: "0"}}, {Keyword: "active"}}}},
			{Query: "price:>100 -spam", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price", Match: &dorks.Match{Operator: dorks.MatchOperatorGreater, Value: "100"}}, {Operator: dorks.OperatorMustNot, Keyword: "spam"}}}},
			{Query: `"first" "second" "third"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "first"}, {Keyword: "second"}, {Keyword: "third"}}}},
			{Query: "rate:1.5 ratio:2.5", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "rate", Match: &dorks.Match{Value: "1.5"}}, {Keyword: "ratio", Match: &dorks.Match{Value: "2.5"}}}}},
			{Query: "birth:1990-05-15 +important", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "birth", Match: &dorks.Match{Value: "1990-05-15"}}, {Operator: dorks.OperatorMust, Keyword: "important"}}}},
			{Query: "price:10 price:20 price:30", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price", Match: &dorks.Match{Value: "10"}}, {Keyword: "price", Match: &dorks.Match{Value: "20"}}, {Keyword: "price", Match: &dorks.Match{Value: "30"}}}}},
			{Query: `"keep" -drop`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "keep"}, {Operator: dorks.OperatorMustNot, Keyword: "drop"}}}},
			{Query: "+\"oferta\" precio:1000000", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "oferta"}, {Keyword: "precio", Match: &dorks.Match{Value: "1000000"}}}}},
			{Query: "1000000 contrato", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "1000000"}, {Keyword: "contrato"}}}},
			{Query: "precio:5000 fecha:2024-12-25 activo", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "precio", Match: &dorks.Match{Value: "5000"}}, {Keyword: "fecha", Match: &dorks.Match{Value: "2024-12-25"}}, {Keyword: "activo"}}}},
			{Query: "100 abc", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "100"}, {Keyword: "abc"}}}},
			{Query: `"a" b`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "a"}, {Keyword: "b"}}}},
			{Query: "   ", Expect: dorks.Query{}},
			{Query: "contrato 2020-01-31", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "contrato"}, {Keyword: "2020-01-31"}}}},
			{Query: `"Hello"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Hello"}}}},
			{Query: `"Hello World"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Hello World"}}}},
			{Query: `"Hello Antonio"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Hello Antonio"}}}},
			{Query: `-"Hello Paula"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "Hello Paula"}}}},
			{Query: `+"keep me"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "keep me"}}}},
			{Query: `""`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: ""}}}},
			{Query: `"with \"escaped\" quotes"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "with \"escaped\" quotes"}}}},
			{Query: "line\nbreak", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "line"}, {Keyword: "break"}}}},
			{Query: "tab\tinside", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "tab"}, {Keyword: "inside"}}}},
			{Query: `"café"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "café"}}}},
			{Query: `"path:with:colons"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "path:with:colons"}}}},
			{Query: `"+not an operator"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "+not an operator"}}}},
			{Query: `":not a field"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: ":not a field"}}}},
			{Query: `"12345"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "12345"}}}},
			{Query: `"2020-01-31"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "2020-01-31"}}}},
			{Query: "created_at:2020-01-31", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "created_at", Match: &dorks.Match{Value: "2020-01-31"}}}}},
			{Query: "updated:1999-12-31", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "updated", Match: &dorks.Match{Value: "1999-12-31"}}}}},
			{Query: "birth:1990-05-15", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "birth", Match: &dorks.Match{Value: "1990-05-15"}}}}},
			{Query: "d:2000-02-29", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "d", Match: &dorks.Match{Value: "2000-02-29"}}}}},
			{Query: `created_at:"2020-01-31"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "created_at", Match: &dorks.Match{Value: "2020-01-31"}}}}},
			{Query: "-created_at:2020-01-31", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "created_at", Match: &dorks.Match{Value: "2020-01-31"}}}}},
			{Query: "price_cop:10", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Value: "10"}}}}},
			{Query: "qty:0", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "qty", Match: &dorks.Match{Value: "0"}}}}},
			{Query: "count:999999", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "count", Match: &dorks.Match{Value: "999999"}}}}},
			{Query: "age:25", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "age", Match: &dorks.Match{Value: "25"}}}}},
			{Query: "price_cop:<10", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Operator: dorks.MatchOperatorLess, Value: "10"}}}}},
			{Query: "price_cop:>10", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Operator: dorks.MatchOperatorGreater, Value: "10"}}}}},
			{Query: "price_cop:<=10", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Operator: dorks.MatchOperatorLessEqual, Value: "10"}}}}},
			{Query: "price_cop:>=10", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_cop", Match: &dorks.Match{Operator: dorks.MatchOperatorGreaterEqual, Value: "10"}}}}},
			{Query: "+price:>100", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "price", Match: &dorks.Match{Operator: dorks.MatchOperatorGreater, Value: "100"}}}}},
			{Query: "-stock:<5", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "stock", Match: &dorks.Match{Operator: dorks.MatchOperatorLess, Value: "5"}}}}},
			{Query: "price_usd:0.99", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_usd", Match: &dorks.Match{Value: "0.99"}}}}},
			{Query: "ratio:1.5", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "ratio", Match: &dorks.Match{Value: "1.5"}}}}},
			{Query: "pi:3.14159", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "pi", Match: &dorks.Match{Value: "3.14159"}}}}},
			{Query: "price_usd:<=0.99", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_usd", Match: &dorks.Match{Operator: dorks.MatchOperatorLessEqual, Value: "0.99"}}}}},
			{Query: "price_usd:>=0.99", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "price_usd", Match: &dorks.Match{Operator: dorks.MatchOperatorGreaterEqual, Value: "0.99"}}}}},
			{Query: "rate:<0.5", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "rate", Match: &dorks.Match{Operator: dorks.MatchOperatorLess, Value: "0.5"}}}}},
			{Query: "rate:>1.0", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "rate", Match: &dorks.Match{Operator: dorks.MatchOperatorGreater, Value: "1.0"}}}}},
			{Query: "+score:>=4.5", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "score", Match: &dorks.Match{Operator: dorks.MatchOperatorGreaterEqual, Value: "4.5"}}}}},
			{Query: "status:active", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "status", Match: &dorks.Match{Value: ("active")}}}}},
			{Query: "type:user", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "type", Match: &dorks.Match{Value: ("user")}}}}},
			{Query: "country:Colombia", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "country", Match: &dorks.Match{Value: ("Colombia")}}}}},
			{Query: "city:Bogota", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "city", Match: &dorks.Match{Value: ("Bogota")}}}}},
			{Query: "name:Antonio", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "name", Match: &dorks.Match{Value: ("Antonio")}}}}},
			{Query: "tag:hot-deal", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "tag", Match: &dorks.Match{Value: ("hot-deal")}}}}},
			{Query: "env:prod_2024", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "env", Match: &dorks.Match{Value: ("prod_2024")}}}}},
			{Query: "status:active and ready", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "status", Match: &dorks.Match{Value: ("active")}}, {Keyword: "and"}, {Keyword: "ready"}}}},
			{Query: "ciudad:Medellín", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "ciudad", Match: &dorks.Match{Value: ("Medellín")}}}}},
			{Query: "+status:active", Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMust, Keyword: "status", Match: &dorks.Match{Value: ("active")}}}}},
			{Query: "tag:<active", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "tag", Match: &dorks.Match{Operator: dorks.MatchOperatorLess, Value: ("active")}}}}},
			{Query: "level:>=high", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "level", Match: &dorks.Match{Operator: dorks.MatchOperatorGreaterEqual, Value: ("high")}}}}},
			{Query: `full-name:"Antonio Donis"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "full-name", Match: &dorks.Match{Value: "Antonio Donis"}}}}},
			{Query: `note:"hello world"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "note", Match: &dorks.Match{Value: "hello world"}}}}},
			{Query: `desc:"with:colon"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "desc", Match: &dorks.Match{Value: "with:colon"}}}}},
			{Query: `q:""`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "q", Match: &dorks.Match{}}}}},
			{Query: "title:\"say \\\"hi\\\"\"", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "title", Match: &dorks.Match{Value: `say "hi"`}}}}},
			{Query: `-author:"Gabriel García"`, Expect: dorks.Query{Dorks: []*dorks.Dork{{Operator: dorks.OperatorMustNot, Keyword: "author", Match: &dorks.Match{Value: "Gabriel García"}}}}},
			{Query: "", Expect: dorks.Query{}},
			{Query: "Hello\n", Expect: dorks.Query{Dorks: []*dorks.Dork{{Keyword: "Hello"}}}},
			{Query: "age:>=30;30.0", Expect: dorks.Query{
				Dorks: []*dorks.Dork{
					{
						Keyword: "age",
						Match:   &dorks.Match{Operator: dorks.MatchOperatorGreaterEqual, Value: "30"},
						Boost:   new(30.0),
					},
				}}},
			{Query: "+Antonio Donis age:<=30 +country:Colombia", Expect: dorks.Query{Dorks: []*dorks.Dork{
				{Operator: dorks.OperatorMust, Keyword: "Antonio"},
				{Keyword: "Donis"},
				{Keyword: "age", Match: &dorks.Match{Operator: dorks.MatchOperatorLessEqual, Value: "30"}},
				{Operator: dorks.OperatorMust, Keyword: "country", Match: &dorks.Match{Value: "Colombia"}},
			}}},
		}
		for _, test := range tests {
			t.Run(test.Query, func(t *testing.T) {
				assertions := assert.New(t)

				q, err := dorks.Parse(strings.NewReader(test.Query))
				if !assertions.NoError(err, "failed to parse query") {
					return
				}

				if !assertions.EqualValues(test.Expect, *q, "values doesn't match") {

					cc, _ := json.MarshalIndent(q, "", "\t")
					t.Logf("Parsed: %s", string(cc))
					return
				}
			})
		}
	})
}
