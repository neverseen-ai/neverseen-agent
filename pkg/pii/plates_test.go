package pii

import (
	"reflect"
	"testing"
)

// The pass has to cut spans where the regular expressions it replaced cut them,
// and each row here is one of the rules they carried — written out, because a
// table derived from the finder would agree with whatever it does.
func TestPlateFinderCutsWhereAPlateBeginsAndEnds(t *testing.T) {
	find := plateFinder(false, "LL-DDD-LL", "LL DDD LL", "LLDDDLL")

	for _, c := range []struct {
		text string
		want []string
		why  string
	}{
		{"le AB-123-CD ici", []string{"AB-123-CD"}, "dashed"},
		{"le AB 123 CD ici", []string{"AB 123 CD"}, "spaced"},
		{"le AB123CD ici", []string{"AB123CD"}, "run together"},
		{"le ab-123-cd ici", []string{"ab-123-cd"}, "lowercase whole"},
		{"AB-123-CD", []string{"AB-123-CD"}, "at both ends of the text"},
		{"(AB-123-CD).", []string{"AB-123-CD"}, "punctuation is a boundary"},
		{"plaque_AB-123-CD", []string{"AB-123-CD"}, "an underscore is a boundary — \\b saw none"},
		{"le AB-123 CD ici", nil, "separators that disagree are two fragments of something else"},
		{"le Ab-123-cD ici", nil, "nobody writes a plate in mixed case"},
		{"éAB-123-CD", nil, "an accented letter glued to it makes one word"},
		{"XAB-123-CD", nil, "a letter glued to it makes one word: XAB is not two letters"},
		{"AB-123-CDE", nil, "nor three letters after"},
		{"AB  123 CD", nil, "two spaces are not a separator"},
		{"AB-123-CD, EF-456-GH", []string{"AB-123-CD", "EF-456-GH"}, "two plates in a list"},
		{"AB-123-CD-EF-456-GH", nil, "two plates joined by a dash are one hyphenated token, not two plates"},
		{"MAC BD-12-FD-34-BF-56", nil, "a fragment of a longer hyphenated token is not a plate"},
		{"SUPERCALIFRAGILISTIC 123 AB", nil, "a chunk longer than a plate's is never part of one"},
	} {
		var got []string
		for _, span := range find(c.text) {
			got = append(got, c.text[span[0]:span[1]])
		}
		if len(got) == 0 && len(c.want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("find(%q) = %q, want %q — %s", c.text, got, c.want, c.why)
		}
	}
}

// upperOnly is for a notation whose lowercase form is any reference code.
func TestPlateFinderCanRefuseLowercase(t *testing.T) {
	find := plateFinder(true, "L-DDDD-LL")
	if got := find("ref M-1234-AB"); len(got) != 1 {
		t.Errorf("an uppercase plate was not found: %v", got)
	}
	if got := find("ref m-1234-ab"); len(got) != 0 {
		t.Errorf("upperOnly found a lowercase plate: %v", got)
	}
}
