package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/cloakfleet/cloakfleet/internal/proxy"
)

// `cloakfleet mask` is the other way to change what the agent masks, and on Linux
// it is the only one: the menu bar is Cocoa, so a workstation without one had the
// route and no way to reach it but curl.
//
// It reads and writes through proxy.Query and proxy.SetPolicy — the one asker and
// the one writer — so this command cannot come to disagree with the menu bar about
// what a category is called or how it is switched. It reads no environment of its
// own either, as nothing in cmd/ does: proxy.ListenAddress owns where the agent is,
// and the control key comes from the file the agent wrote.

const maskUsage = `cloakfleet mask — see and change what the agent masks.

Usage:
  cloakfleet mask                       list every category, by family
  cloakfleet mask --off EMAIL,DOB       stop masking these
  cloakfleet mask --on DOB              mask them again
  cloakfleet mask --off personal        a whole family, by its name
  cloakfleet mask --reset               mask everything again
  cloakfleet mask --substitution fake   change what a masked value is replaced by
  cloakfleet mask --secret-level strong how far down the strength scale to mask
  cloakfleet mask --locales fr,gb       load these country pattern sets
  cloakfleet mask --locales none        load none of them

A category or family that is switched off leaves the machine in clear. Credentials
can never be switched off, whichever way they are named.

--secret-level is weak, medium or strong, and grades only the catch-all pattern:
a credential identified by a prefix ("gsk_", "sk-ant-") is masked at every level.
weak masks every value the pattern finds, ordinary words included, which is what
over-masks source code; strong masks only what nobody typed by hand, and is the
level to run at while reviewing code.

--substitution is token or fake. A token reads as [EMAIL_1] and is obvious in an
answer; a stand-in reads as prose and cannot be told from a real value, which is why
token is the default. Credentials are tokenized either way.

--locales replaces the selection, so name every country you want. With none, only the
locale-independent identifiers and the credentials are found.

Changes last until the agent restarts, deliberately: this is for the exchange you
are looking at, not a policy. What should hold across restarts belongs in
~/.cloakfleet/.env.
`

// runMask lists or changes what is masked.
func runMask(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("mask", flag.ContinueOnError)
	fs.SetOutput(stdout)
	fs.Usage = func() { fmt.Fprint(stdout, maskUsage) }

	off := fs.String("off", "", "categories or families to stop masking, separated by commas")
	on := fs.String("on", "", "categories or families to mask again, separated by commas")
	reset := fs.Bool("reset", false, "mask everything again")
	substitution := fs.String("substitution", "", "what a masked value becomes: token or fake")
	secretLevel := fs.String("secret-level", "", "weakest named secret to mask: weak, medium or strong")
	locales := fs.String("locales", "", "country pattern sets to load, separated by commas, or none")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("mask takes flags, not arguments: %s", strings.Join(fs.Args(), " "))
	}

	addr := proxy.ListenAddress()
	status := proxy.Query(context.Background(), addr, 2*time.Second)
	if !status.Answering {
		// The same answer `status` gives, because it is the same situation: a
		// stopped agent means unmasked traffic rather than a broken workstation, and
		// there is nothing here to change.
		fmt.Fprintf(stdout, "cloakfleet is not answering on %s, so there is nothing to change.\n", addr)
		fmt.Fprint(stdout, "Your tools are reaching their provider directly, unmasked.\n")
		return errQuiet
	}

	if *off == "" && *on == "" && !*reset && *substitution == "" && *locales == "" && *secretLevel == "" {
		writeCatalogue(stdout, status)
		return nil
	}
	if *reset && (*off != "" || *on != "") {
		return fmt.Errorf("--reset masks everything again, so it cannot be combined with --off or --on")
	}

	// The whole state, with whichever parts were named replaced. The route takes it
	// whole rather than patched, because an empty locale list is a valid state and
	// could not otherwise be told from "leave the locales alone".
	want := proxy.PolicyOf(status)

	var err error
	if want.Off, err = newSet(status, *off, *on, *reset); err != nil {
		return err
	}
	if *substitution != "" {
		want.Substitution = *substitution
	}
	if *secretLevel != "" {
		want.SecretLevel = *secretLevel
	}
	if *locales != "" {
		if want.Locales, err = parseLocales(status, *locales); err != nil {
			return err
		}
	}

	after, err := proxy.SetPolicy(context.Background(), addr, proxy.ReadControlKey(""),
		want, 5*time.Second)
	if err != nil {
		return err
	}

	// Printed from the agent's reply rather than from what was asked for, which is
	// also this command's answer to the race it cannot avoid — see newSet.
	writeCatalogue(stdout, after)
	return nil
}

// newSet works out the whole set to send from what the agent currently has.
//
// This is a read-modify-write, and the route it talks to deliberately takes the
// whole set precisely to avoid one. The reason it is acceptable here and not there:
// the hazard is two *surfaces* changing the policy at the same moment, and this is a
// person at a keyboard who has just been shown the state and is about to be shown it
// again. If the menu bar changed something in between, the report this prints
// afterwards says so — which is the same mitigation the menu bar uses when it
// redraws from the agent's reply rather than from its own click.
//
// A name may be a category or a family. Both, because the whole point of grouping
// forty categories is that people think in families, and "--off personal" is what
// somebody means far more often than nine codes. The two namespaces cannot collide:
// a family is lower case, a category upper.
func newSet(status proxy.Status, off, on string, reset bool) ([]string, error) {
	if reset {
		return nil, nil
	}

	set := map[string]bool{}
	for _, code := range status.SwitchedOffCodes() {
		set[code] = true
	}

	for _, name := range splitNames(off) {
		cats, err := resolveName(status, name)
		if err != nil {
			return nil, err
		}
		for _, cat := range cats {
			set[cat] = true
		}
	}
	for _, name := range splitNames(on) {
		cats, err := resolveName(status, name)
		if err != nil {
			return nil, err
		}
		for _, cat := range cats {
			delete(set, cat)
		}
	}

	out := make([]string, 0, len(set))
	for code := range set {
		out = append(out, code)
	}
	sort.Strings(out)
	return out, nil
}

// parseLocales reads a locale selection, refusing a code this agent does not have.
//
// Refused here as well as by the agent, and the reason is the message: the agent
// answers with what its registry holds, and so does this — but a person who typed
// "uk" learns it from the command they ran rather than from an HTTP status.
//
// "none" is spelled out rather than expressed as an empty argument, matching
// CLOAKFLEET_PII_LOCALE: `--locales ""` is what a shell produces from an unset
// variable by accident, and it must not silently mean "stop looking for anything".
func parseLocales(status proxy.Status, spec string) ([]string, error) {
	if strings.EqualFold(strings.TrimSpace(spec), "none") {
		return []string{}, nil
	}

	available := map[string]string{}
	for _, code := range status.AvailableLocales {
		available[strings.ToLower(code)] = code
	}

	out := []string{}
	for _, name := range splitNames(spec) {
		code, ok := available[strings.ToLower(name)]
		if !ok {
			return nil, fmt.Errorf("no country pattern set %q — this agent has %s, or none",
				name, strings.Join(status.AvailableLocales, ", "))
		}
		out = append(out, code)
	}
	return out, nil
}

// resolveName turns one name into the categories it stands for.
//
// A locked category named directly is an error rather than a silent skip: somebody
// typing "--off SECRET_ANTHROPIC_KEY" has a belief about what this agent will do,
// and the only useful answer is that it will not. Named through its family it is
// skipped instead — "--off secrets" is a reasonable thing to try, and refusing the
// whole request over it would leave nothing switched.
func resolveName(status proxy.Status, name string) ([]string, error) {
	for _, g := range status.Groups {
		if !strings.EqualFold(g.Code, name) {
			continue
		}
		var out []string
		for _, c := range g.Categories {
			if !c.Locked {
				out = append(out, c.Code)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("nothing in %q can be switched off: %s", g.Code, lockedReason(g.Code))
		}
		return out, nil
	}

	for _, g := range status.Groups {
		for _, c := range g.Categories {
			if !strings.EqualFold(c.Code, name) {
				continue
			}
			if c.Locked {
				return nil, fmt.Errorf("%s cannot be switched off: %s", c.Code, lockedReason(g.Code))
			}
			return []string{c.Code}, nil
		}
	}

	return nil, fmt.Errorf("no category or family named %q — `cloakfleet mask` lists them", name)
}

// lockedReason says which rule refused, because a bare refusal leaves somebody
// guessing between a bug and a policy.
func lockedReason(group string) string {
	if group == "declared" {
		return "it is what this deployment declared sensitive itself"
	}
	return "a credential in clear is a live key handed to a provider"
}

func splitNames(spec string) []string {
	var out []string
	for _, part := range strings.Split(spec, ",") {
		if name := strings.TrimSpace(part); name != "" {
			out = append(out, name)
		}
	}
	return out
}

// writeCatalogue prints every family and what is happening to each category.
//
// The first column is a word rather than a tick, so `cloakfleet mask | grep "in
// clear"` answers the question this command exists for. A symbol would need a legend
// and would not survive a pipe into anything.
func writeCatalogue(w io.Writer, status proxy.Status) {
	// The three states, and the first one matters most: with no country pattern set
	// loaded the agent is up and recognises almost nothing, and a header saying
	// "every category is on" over that would be true and useless — every category of
	// a catalogue that holds almost nothing.
	//
	// The sentence comes from proxy.Status rather than from here, because this
	// command and `cloakfleet status` are two surfaces reporting on one agent: the
	// hand-written pair had already drifted, one calling a category "switched off"
	// where the other called it "in clear". What follows it is this command's own,
	// since only this one is about to list the catalogue underneath.
	fmt.Fprintf(w, "%s\n\n", status.Headline())

	if len(status.Locales) == 0 {
		fmt.Fprint(w, "No country pattern set is loaded, so only the locale-independent\n")
		fmt.Fprint(w, "identifiers and the credentials below are recognised at all.\n\n")
	}

	fmt.Fprintf(w, "  substitution   %s\n", or(status.Substitution, "unknown"))
	fmt.Fprintf(w, "  secret level   %s\n", or(status.SecretLevel, "unknown"))
	fmt.Fprintf(w, "  countries      %s   (of %s)\n\n",
		or(strings.Join(status.Locales, ", "), "none"),
		strings.Join(status.AvailableLocales, ", "))

	for _, g := range status.Groups {
		locked := true
		for _, c := range g.Categories {
			if !c.Locked {
				locked = false
			}
		}

		fmt.Fprintf(w, "  %s", g.Label)
		if locked {
			fmt.Fprint(w, "  (never switched off here)")
		} else {
			fmt.Fprintf(w, "  — %s", g.Code)
		}
		fmt.Fprint(w, "\n")

		for _, c := range g.Categories {
			state := "masked"
			if c.Off {
				state = "in clear"
			}
			fmt.Fprintf(w, "    %-9s %-24s %s\n", state, c.Code, c.Label)
		}
		fmt.Fprint(w, "\n")
	}

	fmt.Fprint(w, "Switch one off with `cloakfleet mask --off CODE`, a whole family by its name.\n")
	fmt.Fprint(w, "Change the rest with `--substitution token|fake`, `--secret-level weak|medium|strong`\n"+
		"and `--locales fr,gb|none`.\n")
}

func or(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
