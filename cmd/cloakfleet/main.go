// Command cloakfleet is the agent: one binary, one pipeline.
//
// It is deliberately a single entrypoint. The project this one replaces shipped
// two, each assembling its own pipeline from the same packages, and they
// drifted: one applied a masking rule on the way back and the other did not, so
// the same request was masked in one and answered in clear in the other. A
// second entrypoint is a second answer to "what does this agent do", and the
// two will disagree eventually.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

// version is stamped at build time with -ldflags "-X main.version=…".
//
// A var, not a const: Agent Veil declared it const, which makes the linker flag
// silently inert, and every release it built reported the same string.
var version = "dev"

const usage = `cloakfleet — mask sensitive values before they reach a model.

Usage:
  cloakfleet scan [file]   report the sensitive values in a file, or in stdin
  cloakfleet version       print the version

Environment:
  %s    country pattern sets to load: %s, none,
%s    or a comma-separated list. Unset means none: only the
%s    locale-independent identifiers and the credentials.
  %s comma-separated values never to mask.
`

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "cloakfleet:", err)
		os.Exit(1)
	}
}

// run is main's body with its inputs and output passed in, so the commands are
// testable without a subprocess.
func run(args []string, stdin io.Reader, stdout io.Writer) error {
	if len(args) == 0 {
		printUsage(stdout)
		return errors.New("no command given")
	}

	switch cmd := args[0]; cmd {
	case "scan":
		return runScan(args[1:], stdin, stdout)
	case "version":
		fmt.Fprintln(stdout, version)
		return nil
	case "-h", "--help", "help":
		printUsage(stdout)
		return nil
	default:
		printUsage(stdout)
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func printUsage(w io.Writer) {
	pad := strings.Repeat(" ", len(detector.EnvLocale)+2)
	fmt.Fprintf(w, usage,
		detector.EnvLocale, strings.Join(pii.LocaleCodes(), ", "),
		pad, pad,
		detector.EnvAllowList)
}

func runScan(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stdout)
	if err := fs.Parse(args); err != nil {
		return err
	}

	text, err := readInput(fs.Args(), stdin)
	if err != nil {
		return err
	}

	d, err := detector.FromEnv()
	if err != nil {
		return err
	}

	matches := d.Scan(text)
	report(stdout, d, matches)
	return nil
}

// readInput reads the named file, or stdin when no file is named.
func readInput(args []string, stdin io.Reader) (string, error) {
	if len(args) > 1 {
		return "", fmt.Errorf("scan takes at most one file, got %d", len(args))
	}
	if len(args) == 0 {
		raw, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(raw), nil
	}

	raw, err := os.ReadFile(filepath.Clean(args[0]))
	if err != nil {
		return "", fmt.Errorf("read %s: %w", args[0], err)
	}
	return string(raw), nil
}

// report prints what would be masked, then a count per category.
//
// The per-category count is the line that matters to an operator checking their
// own data is covered: a value they expected to see named, missing from the
// tally, is a gap in the catalogue for their data shape.
func report(w io.Writer, d *detector.Detector, matches []detector.Match) {
	locales := "none"
	if l := d.Locales(); len(l) > 0 {
		locales = strings.Join(l, ",")
	}
	fmt.Fprintf(w, "locales: %s\n\n", locales)

	if len(matches) == 0 {
		fmt.Fprintln(w, "no sensitive values found")
		return
	}

	counts := map[pii.Category]int{}
	for _, m := range matches {
		counts[m.Category]++
		fmt.Fprintf(w, "%-24s %s\n", m.Category, m.Value)
		fmt.Fprintf(w, "%-24s   %s, bytes %d-%d, confidence %d\n", "", m.Label, m.Start, m.End, m.Confidence)
	}

	cats := make([]string, 0, len(counts))
	for cat := range counts {
		cats = append(cats, string(cat))
	}
	sort.Strings(cats)

	fmt.Fprintf(w, "\n%d value(s) would be masked:\n", len(matches))
	for _, cat := range cats {
		fmt.Fprintf(w, "  %-24s %d\n", cat, counts[pii.Category(cat)])
	}
}
