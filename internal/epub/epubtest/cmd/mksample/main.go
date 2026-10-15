// Command mksample writes epubtest.Sample to stdout:
//
//	go run ./internal/epub/epubtest/cmd/mksample > hack/testdata/sample.epub
package main

import (
	"fmt"
	"os"

	"github.com/yoadey/audiobook-generator/internal/epub/epubtest"
)

func main() {
	data, err := epubtest.Sample()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(data)
}
