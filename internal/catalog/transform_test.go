package catalog_test

import (
	"reflect"
	"testing"

	"github.com/RXWatcher/continuum-plugin-bookwarehouse-ebook/internal/bookwarehouse"
	"github.com/RXWatcher/continuum-plugin-bookwarehouse-ebook/internal/catalog"
)

func TestToSummary_HappyPath(t *testing.T) {
	in := bookwarehouse.Book{
		ID: "bw-1", Title: "Atlas Shrugged",
		Authors: []string{"Ayn Rand"}, Year: 1957, Rating: 4.2,
		CoverURL: "https://upstream/c/1", HasCover: true,
		Formats: []string{"epub", "pdf"},
	}
	got := catalog.ToSummary(in)
	want := catalog.EbookSummary{
		ID: "bw-1", Title: "Atlas Shrugged",
		Authors: []string{"Ayn Rand"}, Year: 1957,
		CoverURL: "https://upstream/c/1", HasCover: true, Rating: 4.2,
		Formats: []string{"epub", "pdf"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ToSummary: got %+v want %+v", got, want)
	}
}

func TestToDetail_IncludesFilesWithMime(t *testing.T) {
	in := bookwarehouse.BookDetail{
		Book: bookwarehouse.Book{ID: "bw-2", Title: "X", Formats: []string{"epub"}},
		Files: []bookwarehouse.File{
			{Format: "epub", SizeBytes: 1024},
		},
	}
	got := catalog.ToDetail(in)
	if len(got.Files) != 1 || got.Files[0].Format != "epub" || got.Files[0].MimeType != "application/epub+zip" {
		t.Errorf("files: %+v", got.Files)
	}
	if got.UpstreamID != "bw-2" {
		t.Errorf("upstream_id = %q", got.UpstreamID)
	}
}

func TestFormatToMime(t *testing.T) {
	cases := map[string]string{
		"epub": "application/epub+zip",
		"pdf":  "application/pdf",
		"mobi": "application/x-mobipocket-ebook",
		"azw3": "application/vnd.amazon.ebook",
		"":     "application/octet-stream",
		"xyz":  "application/octet-stream",
	}
	for f, want := range cases {
		if got := catalog.FormatToMime(f); got != want {
			t.Errorf("FormatToMime(%q) = %q, want %q", f, got, want)
		}
	}
}
