package main

import (
	"bufio"
	"compress/bzip2"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"iter"
	"log"
	"net/http"
	"os"

	"github.com/RogueTeam/textiplex/pool"
	"github.com/RogueTeam/textiplex/testsuite/wikipedia"
)

const (
	TargetUrl  = "https://dumps.wikimedia.org/enwiki/latest/enwiki-latest-pages-articles.xml.bz2"
	JsonFile   = "enwiki-latest-pages-articles.json"
	TargetFile = "enwiki-latest-pages-articles.xml.bz2"
)

func downloadData() {
	file, err := os.Create(TargetFile)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	bufferedW := bufio.NewWriterSize(file, 1024*1024)

	res, err := http.Get(TargetUrl)
	if err != nil {
		log.Fatal(err)
	}
	defer res.Body.Close()

	bufferedR := bufio.NewReaderSize(res.Body, 1024*1024)

	_, err = io.Copy(bufferedW, bufferedR)
	if err != nil {
		log.Fatal(err)
	}

	err = bufferedW.Flush()
	if err != nil {
		log.Fatal(err)
	}
}

type MediaWikiDump struct {
	Pages []Page `xml:"page"`
}

type Page struct {
	Title     []byte     `xml:"title"`
	ID        int64      `xml:"id"`
	Revisions []Revision `xml:"revision"`
}

type Revision struct {
	Text RevisionText `xml:"text"`
}

type RevisionText struct {
	Text []byte `xml:",chardata"`
}

func Pages() (seq iter.Seq[*Page], err error) {
	file, err := os.Open(TargetFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open file defined by: %s: %w", TargetFile, err)
	}
	return func(yield func(*Page) bool) {
		defer file.Close()

		buffered := bufio.NewReaderSize(file, 1024*1024)

		bz := bzip2.NewReader(buffered)
		dec := xml.NewDecoder(bz)

		pagePool := pool.New[Page](20)
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			se, ok := tok.(xml.StartElement)
			if !ok || se.Name.Local != "page" {
				continue
			}

			var page = pagePool.Get()
			if err := dec.DecodeElement(page, &se); err != nil {
				continue
			}

			if !yield(page) {
				return
			}
		}
	}, nil
}

func convertToJson() {
	file, err := os.Create(JsonFile)
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	buffered := bufio.NewWriterSize(file, 1024*1024)

	pages, err := Pages()
	if err != nil {
		log.Fatal(err)
	}

	encoder := json.NewEncoder(buffered)

	var wPage wikipedia.Page
	for page := range pages {
		if len(page.Revisions) == 0 {
			continue
		}

		last := len(page.Revisions) - 1

		wPage = wikipedia.Page{
			Id:      page.ID,
			Title:   page.Title,
			Content: page.Revisions[last].Text.Text,
		}

		encoder.Encode(&wPage)
	}

	err = buffered.Flush()
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	_, err := os.Stat(TargetFile)
	if err != nil {
		if os.IsExist(err) {
			log.Fatal(err)
		}

		if os.IsNotExist(err) {
			downloadData()
		}
	}

	_, err = os.Stat(JsonFile)
	if err != nil {
		if os.IsExist(err) {
			log.Fatal(err)
		}

		if os.IsNotExist(err) {
			convertToJson()
		}
	}

}
