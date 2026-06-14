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
	for page := range pages {
		fmt.Println(string(page.Title))
	}

	t.Logf("Spent: %v", time.Since(start))
}
