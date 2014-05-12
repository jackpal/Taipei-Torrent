package tracker

import (
	"testing"
)

func TestScrapeURL(t *testing.T) {
	tests := []struct{ announce, scrape string }{
		{"", ""},
		{"foo", ""},
		{"x/announce", "x/scrape"},
		{"x/announce?ad#3", "x/scrape?ad#3"},
		{"announce/x", ""},
	}
	for _, test := range tests {
		scrape := ScrapePattern(test.announce)
		if scrape != test.scrape {
			t.Errorf("ScrapeURL(%#v) = %#v. Expected %#v", test.announce, scrape, test.scrape)
		}
	}
}
