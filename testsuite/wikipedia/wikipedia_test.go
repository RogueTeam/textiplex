package wikipedia_test

import (
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
		if count%10_000 == 0 {
			t.Logf("Delta to: %d - %s - %v", count, string(page.Title), time.Since(start))
			break
		}
	}

	t.Logf("Spent: %v", time.Since(start))
}
