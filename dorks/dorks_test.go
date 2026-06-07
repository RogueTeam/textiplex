package dorks_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/RogueTeam/textiplex/dorks"
	"github.com/stretchr/testify/assert"
)

func TestParse(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		type Test struct {
			Name    string
			Queries []string
		}
		tests := []Test{
			{Name: "Keywords", Queries: []string{"Hello"}},
			{Name: "Operators", Queries: []string{"+Hello", "-Hello", "-1000"}},
			{Name: "Phrase", Queries: []string{`"Hello Antonio"`, `-"Hello Paula"`}},
			{Name: "Fields", Queries: []string{
				`created_at:2020-01-31`,
				`created_at:"2020-01-31"`,
				`price_cop:10`,
				`price_usd:0.99`,
				`price_cop:<10`,
				`price_usd:<=0.99`,
				`price_cop:>10`,
				`price_usd:>=0.99`,
				`full-name:"Antonio Donis"`,
			}},
		}
		for _, test := range tests {
			t.Run(test.Name, func(t *testing.T) {
				for _, query := range test.Queries {
					t.Run(query, func(t *testing.T) {
						assertions := assert.New(t)

						q, err := dorks.Parse(strings.NewReader(query))
						if !assertions.Nil(err, "failed to parse query") {
							return
						}

						cc, _ := json.MarshalIndent(q, "", "\t")
						t.Logf("Parsed: %s", string(cc))
					})
				}
			})
		}
	})
}
