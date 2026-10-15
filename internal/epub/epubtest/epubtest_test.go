package epubtest

import (
	"bytes"
	"os"
	"testing"
)

// The e2e test uploads hack/testdata/sample.epub; keep it in sync.
func TestSampleFixtureIsUpToDate(t *testing.T) {
	want, err := Sample()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../../hack/testdata/sample.epub")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("hack/testdata/sample.epub is stale: go run ./internal/epub/epubtest/cmd/mksample > hack/testdata/sample.epub")
	}
}
