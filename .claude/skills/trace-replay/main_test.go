package main

import (
	"strings"
	"testing"
)

// The comparator is what decides SAME, so each fault a replay must catch is injected
// here — and the cases that must stay silent are half the suite: a comparator that
// reports everything passes every fault.
//
// go test ./.claude/skills/trace-replay
func TestCompare(t *testing.T) {
	in := `{
  "a": "mail bob@example.com and alice@example.com",
  "b": "bob@example.com again, and the model's own [EMAIL_1]"
}`
	recorded := `{
  "a": "mail [EMAIL_7] and [EMAIL_8]",
  "b": "[EMAIL_7] again, and the model's own [EMAIL_1]"
}`
	cases := []struct {
		name, replayed, want string // want "" means identical
	}{
		{"renumbered", `{
  "a": "mail [EMAIL_2] and [EMAIL_3]",
  "b": "[EMAIL_2] again, and the model's own [EMAIL_1]"
}`, ""},
		{"a fresh vault mints a string the tool sent as text", `{
  "a": "mail [EMAIL_1] and [EMAIL_2]",
  "b": "[EMAIL_1] again, and the model's own [EMAIL_1]"
}`, ""},
		{"a value no longer masked", `{
  "a": "mail [EMAIL_2] and alice@example.com",
  "b": "[EMAIL_2] again, and the model's own [EMAIL_1]"
}`, "no longer masked  EMAIL          alice@example.com"},
		{"a value newly masked", `{
  "a": "mail [EMAIL_2] and [EMAIL_3]",
  "b": "[EMAIL_2] again, and [PERSON_1] own [EMAIL_1]"
}`, "newly masked      PERSON         the model's"},
		{"one value given two names", `{
  "a": "mail [EMAIL_2] and [EMAIL_3]",
  "b": "[EMAIL_4] again, and the model's own [EMAIL_1]"
}`, "line 3"},
		{"two values given one name", `{
  "a": "mail [EMAIL_2] and [EMAIL_2]",
  "b": "[EMAIL_2] again, and the model's own [EMAIL_1]"
}`, "line 2"},
		{"text around a replacement moved", `{
  "a": "mail [EMAIL_2] and [EMAIL_3] ",
  "b": "[EMAIL_2] again, and the model's own [EMAIL_1]"
}`, "text around the replacements changed"},
		{"keys reordered", `{
  "b": "[EMAIL_2] again, and the model's own [EMAIL_1]",
  "a": "mail [EMAIL_2] and [EMAIL_3]"
}`, "line 2"},
		{"shape changed", `{
  "a": "mail [EMAIL_2] and [EMAIL_3]"
}`, "changed shape"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := strings.Join(compare(trace{in: in, out: recorded}, c.replayed, 5).diffs, "")
			if c.want == "" && got != "" {
				t.Fatalf("want identical, got:\n%s", got)
			}
			if c.want != "" && !strings.Contains(got, c.want) {
				t.Fatalf("want a report containing %q, got:\n%s", c.want, got)
			}
		})
	}
}

func TestParseStopsTheOutboundBodyAtTheResponseHeader(t *testing.T) {
	tr, err := parse("session:  default\nprovider: anthropic\n\n── IN   from the tool ──\n{\n  \"a\": 1\n}\n\n" +
		"── OUT  to anthropic ──\n{\n  \"a\": 1\n}\nback:       12 bytes\nmodel:      x\n\n── BACK from anthropic ──\n{}\n")
	if err != nil {
		t.Fatal(err)
	}
	if tr.provider != "anthropic" || tr.in != "{\n  \"a\": 1\n}" || tr.out != tr.in {
		t.Fatalf("parsed %+v", tr)
	}
}

func TestDoubts(t *testing.T) {
	in := `{
  "a": "mail bob@example.com, and the model's own [EMAIL_1]"
}`
	cases := []struct {
		name, in, recorded, replayed, want string // want "" means no doubt
	}{
		{"numbered past what the tool sent", in, `{
  "a": "mail [EMAIL_7], and the model's own [EMAIL_1]"
}`, `{
  "a": "mail [EMAIL_2], and the model's own [EMAIL_1]"
}`, ""},
		{"a fresh session mints what the tool sent", in, `{
  "a": "mail [EMAIL_7], and the model's own [EMAIL_1]"
}`, `{
  "a": "mail [EMAIL_1], and the model's own [EMAIL_1]"
}`, "[EMAIL_1] was minted for a value (replay)"},
		{"the live agent minted what the tool sent", in, `{
  "a": "mail [EMAIL_1], and the model's own [EMAIL_1]"
}`, `{
  "a": "mail [EMAIL_2], and the model's own [EMAIL_1]"
}`, "[EMAIL_1] was minted for a value (recorded)"},
		{"stand-ins and no token", `{
  "a": "mail bob@example.com"
}`, `{
  "a": "mail jane@example.org"
}`, `{
  "a": "mail jane@example.org"
}`, "likely fake mode"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := compare(trace{in: c.in, out: c.recorded}, c.replayed, 5)
			if len(got.diffs) > 0 {
				t.Fatalf("want identical, got:\n%s", strings.Join(got.diffs, ""))
			}
			doubts := strings.Join(got.doubts, "")
			if c.want == "" && doubts != "" {
				t.Fatalf("want no doubt, got:\n%s", doubts)
			}
			if c.want != "" && !strings.Contains(doubts, c.want) {
				t.Fatalf("want a doubt containing %q, got:\n%s", c.want, doubts)
			}
		})
	}
}
