package aiprovider

import "testing"

func TestParseSuggestionsJSONPlain(t *testing.T) {
	got, err := parseSuggestionsJSON(`[{"title":"Send the report","project":"Q4 Planning","tags":["urgent"],"priority":"h","due":"friday"}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d suggestions, want 1", len(got))
	}
	s := got[0]
	if s.Title != "Send the report" || s.Project != "Q4 Planning" || s.Priority != "h" || s.Due != "friday" {
		t.Fatalf("parsed = %+v", s)
	}
	if len(s.Tags) != 1 || s.Tags[0] != "urgent" {
		t.Fatalf("tags = %v", s.Tags)
	}
}

func TestParseSuggestionsJSONWithSurroundingText(t *testing.T) {
	// Models sometimes ignore "respond with ONLY a JSON array" — the parser
	// must still find the array amid a markdown fence and commentary.
	text := "Here are the action items:\n```json\n[{\"title\":\"Do the thing\",\"project\":\"\",\"tags\":[],\"priority\":\"m\",\"due\":\"\"}]\n```\nLet me know if you need more."
	got, err := parseSuggestionsJSON(text)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Do the thing" {
		t.Fatalf("got = %+v", got)
	}
}

func TestParseSuggestionsJSONEmptyArray(t *testing.T) {
	got, err := parseSuggestionsJSON("[]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d suggestions, want 0", len(got))
	}
}

func TestParseSuggestionsJSONNoArrayFound(t *testing.T) {
	if _, err := parseSuggestionsJSON("I couldn't find any action items."); err == nil {
		t.Fatal("expected an error when no JSON array is present")
	}
}

func TestParseSuggestionsJSONDropsBlankTitles(t *testing.T) {
	got, err := parseSuggestionsJSON(`[{"title":"","project":"","tags":[],"priority":"m","due":""},{"title":"Real item","project":"","tags":[],"priority":"m","due":""}]`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "Real item" {
		t.Fatalf("got = %+v, want only the non-blank title", got)
	}
}

func TestNormalizePriority(t *testing.T) {
	cases := map[string]string{
		"h": "h", "high": "h", "H": "h", "High": "h",
		"l": "l", "low": "l", "L": "l",
		"m": "m", "medium": "m", "": "m", "whatever": "m",
	}
	for in, want := range cases {
		if got := normalizePriority(in); got != want {
			t.Errorf("normalizePriority(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanTags(t *testing.T) {
	got := cleanTags([]string{" Urgent ", "", "Vendor", "  "})
	if len(got) != 2 || got[0] != "urgent" || got[1] != "vendor" {
		t.Fatalf("cleanTags = %v", got)
	}
}
