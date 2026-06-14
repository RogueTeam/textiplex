package wikipedia_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/RogueTeam/textiplex/testsuite/wikipedia"
	"github.com/stretchr/testify/assert"
)

func TestPages(t *testing.T) {
	assertions := assert.New(t)

	pages, err := wikipedia.Pages()
	if !assertions.NoError(err, "should retrieve pages") {
		return
	}

	start := time.Now()

	var count int
	for page := range pages {
		count++
		if count%1_000_000 == 0 {
			fmt.Println(count, string(page.Title))
		}
	}

	t.Logf("Spent: %v", time.Since(start))
}
