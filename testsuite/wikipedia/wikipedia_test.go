package wikipedia_test

import (
	"testing"
	"time"

	"github.com/RogueTeam/textiplex/testsuite/wikipedia"
)

func TestPages(t *testing.T) {
	pages, err := wikipedia.Pages()
	if err != nil {
		t.Skipf("Wikipedia sample is not ready use go generate over the repository to retrieve it: go generate ./...")
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
