package pii

import (
	"reflect"
	"strconv"
	"strings"
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

// A malformed notation panics with a sentence naming it, not with a runtime
// error: the panic fires at start-up, and "slice bounds out of range" says
// nothing about which of forty countries to open. An unclosed repetition was
// sliced before it was checked.
func TestAMalformedSignaturePanicsWithItsName(t *testing.T) {
	for _, notation := range []string{
		"LL-D{1,2",  // unclosed repetition
		"LL-(D|DD",  // unclosed group
		"LL-D{2,1}", // a range that runs backwards
		"LL-D{,2}",  // a range with no lower bound
		"LL-DDD-Ll", // a lowercase letter, which no text's signature has
		"LL-DDD-LE", // a literal letter, ditto
	} {
		func() {
			defer func() {
				r := recover()
				msg, ok := r.(string)
				if !ok || !strings.HasPrefix(msg, "pii: ") || !strings.Contains(msg, strconv.Quote(notation)) {
					t.Errorf("expandSignatures(%q) panicked with %v, want a message naming the notation", notation, r)
				}
			}()
			expandSignatures(notation)
		}()
	}
}

// plateFinder expands every signature it is handed, so one written out bare is
// checked like a compact one. Before, only the compact ones were: a bare
// signature with a typo matched no text, and the country's plates went out in
// clear with nothing to say so. It relies on a bare signature expanding to
// itself, which is the second half.
func TestPlateFinderRefusesABareSignatureWithATypo(t *testing.T) {
	func() {
		defer func() {
			if recover() == nil {
				t.Error(`plateFinder took "LL-DDD-Ll", a signature no text has`)
			}
		}()
		plateFinder(false, "LL-DDD-Ll")
	}()

	for _, bare := range []string{"LL-DDD-LL", "LL DDD LL", "LLDDDLL", "D-LLL-DDD", "L DD LLL"} {
		if got := expandSignatures(bare); !reflect.DeepEqual(got, []string{bare}) {
			t.Errorf("expandSignatures(%q) = %q, want it unchanged", bare, got)
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
