package wikipedia

import (
	"bytes"
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"iter"
	"os"

	"github.com/RogueTeam/textiplex/pool"
	"golang.org/x/sys/unix"
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

	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	data, err := unix.Mmap(
		int(file.Fd()),
		0,
		int(info.Size()),
		unix.PROT_READ,
		unix.MAP_PRIVATE)
	if err != nil {
		return nil, fmt.Errorf("failed to mmap file: %w", err)
	}

	return func(yield func(*Page) bool) {
		defer func() {
			unix.Munmap(data)
			file.Close()
		}()

		var poolPage = pool.New[Page](1_000)
		for line := range bytes.SplitSeq(data, []byte("\n")) {
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
