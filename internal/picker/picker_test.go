package picker

import (
	"strings"
	"testing"
)

func items(labels ...string) []Item {
	out := make([]Item, len(labels))
	for i, l := range labels {
		out[i] = Item{Label: l}
	}
	return out
}

func TestPickNumberAndDefault(t *testing.T) {
	var out strings.Builder
	got, err := promptPick(strings.NewReader("2\n"), &out, "pick", items("a", "b", "c"), 0)
	if err != nil || got != 1 {
		t.Fatalf("got %d, %v", got, err)
	}
	got, err = promptPick(strings.NewReader("\n"), &out, "pick", items("a", "b"), 1)
	if err != nil || got != 1 {
		t.Fatalf("default: got %d, %v", got, err)
	}
}

func TestPickFilterThenNumber(t *testing.T) {
	var out strings.Builder
	// "ep" filters to episode2/episode10; "2" then picks the second visible
	// (episode10, original index 2).
	got, err := promptPick(strings.NewReader("ep\n2\n"), &out,
		"pick", items("intro", "episode2", "episode10"), -1)
	if err != nil || got != 2 {
		t.Fatalf("got %d, %v", got, err)
	}
	if !strings.Contains(out.String(), "episode10") {
		t.Error("filtered list not shown")
	}
}

func TestPickMultiRangesAndAll(t *testing.T) {
	var out strings.Builder
	got, err := promptPickMulti(strings.NewReader("1,3-4\n"), &out, "pick", items("a", "b", "c", "d", "e"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0] != 0 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("got %v", got)
	}
	got, err = promptPickMulti(strings.NewReader("all\n"), &out, "pick", items("a", "b"))
	if err != nil || len(got) != 2 {
		t.Fatalf("all: %v, %v", got, err)
	}
}

func TestPickMultiFilterScopesSelection(t *testing.T) {
	var out strings.Builder
	// Filter to srt files, then take all of the filtered view.
	got, err := promptPickMulti(strings.NewReader("srt\nall\n"), &out,
		"pick", items("movie.mkv", "en.srt", "ru.srt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestOutOfRangeIsFilterNotCrash(t *testing.T) {
	var out strings.Builder
	// "9" is out of range → treated as a filter (no match → full list), then 1.
	got, err := promptPick(strings.NewReader("9\n1\n"), &out, "pick", items("a", "b"), -1)
	if err != nil || got != 0 {
		t.Fatalf("got %d, %v", got, err)
	}
}
