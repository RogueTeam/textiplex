package wikipedia

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"fmt"
	"iter"
	"log"
	"os"

	"github.com/RogueTeam/textiplex/pool"
)

const WikipediaFilenameVar = "WIKIPEDIA_FILENAME"

var WikipediaFilename = os.Getenv(WikipediaFilenameVar)

type Page struct {
	Id      int64
	Title   []byte
	Content []byte
}

func Pages() (seq iter.Seq[*Page], err error) {
	if WikipediaFilename == "" {
		return nil, fmt.Errorf("%s not set", WikipediaFilenameVar)
	}

	file, err := os.Open(WikipediaFilename)
	if err != nil {
		return nil, fmt.Errorf("failed to open file defined by: %s: %w", WikipediaFilename, err)
	}
	return func(yield func(*Page) bool) {
		defer file.Close()

		buffered := bufio.NewReaderSize(file, 1024*1024)

		scanner := bufio.NewScanner(buffered)

		var poolPage = pool.New[Page](1_000)
		for scanner.Scan() {
			line := scanner.Bytes()

			page := poolPage.Get()
			json.Unmarshal(line, page)

			if !yield(page) {
				return
			}
		}

		err := scanner.Err()
		if err != nil {
			log.Printf("failed to scan json file: %v", err)
		}
	}, nil
}
