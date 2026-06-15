package wikipedia

import (
	"bufio"
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"iter"
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
	defer func() {
		if err != nil {
			file.Close()
		}
	}()

	return func(yield func(*Page) bool) {
		defer func() {
			file.Close()
		}()

		buffered := bufio.NewReaderSize(file, 1024*1024)

		scanner := bufio.NewScanner(buffered)
		scanner.Buffer(make([]byte, 5*1024*1024), 5*1024*1024)
		scanner.Split(bufio.ScanLines)

		var poolPage = pool.New[Page](1_000)
		for scanner.Scan() {
			line := scanner.Bytes()

			if len(line) == 0 {
				continue
			}

			page := poolPage.Get()
			err := json.Unmarshal(line, page)
			if err != nil {
				continue
			}

			if !yield(page) {
				return
			}
		}
	}, nil
}
