package proxy

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/cloakfleet/cloakfleet/internal/detector"
	"github.com/cloakfleet/cloakfleet/pkg/pii"
)

// What a surface changed survives the restart, and the file is the state.
//
// Without this, everything the menu bar can do lasts until the agent stops: a
// person unticks a category, restarts the workstation, and the agent comes back
// masking it again while the menu they set still says otherwise on the next click.
// A control whose settings are forgotten is one nobody can rely on having set.
//
// The file holds the same four fields PUT /policy carries, in the same shape,
// because it *is* that request: what the agent would have to be sent to arrive
// where it is. One document rather than a field per file, for the reason the route
// replaces the whole state rather than patching it — a half-applied selection is a
// state nobody asked for.
//
// It wins over the environment, and that is the point rather than an accident. The
// environment configures an agent nobody has said anything to yet; once a surface
// has written a policy, that policy is the state, or the click does not survive the
// restart and this file has no purpose. The cost is real and named in the
// documentation: with the file present, changing CLOAKFLEET_PII_LOCALE in a profile
// does nothing. Deleting the file hands the agent back to the environment, and the
// agent says at start-up which of the two it read.

// DefaultPolicyFile is where a running agent keeps what it was last told to mask.
//
// Beside the control key, under the directory the installer deliberately leaves
// alone on uninstall: it is the same kind of thing, state this machine holds that
// nobody types by hand.
const DefaultPolicyFile = "~/.cloakfleet/policy.json"

// loadPolicyFile reads the stored state, or reports nil when there is none.
//
// Nil and no error for an absent file: an agent that has never been told anything
// is the ordinary case, not a degraded one. A file that cannot be read or parsed is
// an error, and the caller keeps what the environment configured — the safe
// direction, since the alternative is starting with a state read from half a
// document.
func loadPolicyFile(path string) (*policyRequest, error) {
	resolved, err := expandHome(path)
	if err != nil {
		return nil, err
	}

	raw, err := os.ReadFile(filepath.Clean(resolved))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", resolved, err)
	}

	var stored policyRequest
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, fmt.Errorf("read %s: %w", resolved, err)
	}
	return &stored, nil
}

// writePolicyFile stores the state where only its owner can read it.
//
// The same treatment the control key gets — directory 0700, file 0600, written by
// rename — for a weaker reason than a secret but a real one: it says which
// categories this workstation stopped masking, which is a description of what its
// user handles. Rename because a torn file on the next start reads as unparseable,
// and an agent that cannot read this one goes back to the environment: a crash
// mid-write would silently undo the change it was recording.
func writePolicyFile(path string, state policyRequest) error {
	resolved, err := expandHome(path)
	if err != nil {
		return err
	}

	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(resolved)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	// A name of its own for each write, and not a fixed ".tmp".
	//
	// Two surfaces write this file — the menu bar and `cloakfleet mask`, and they
	// interleave, which is the hazard the route already names for the detector. On
	// one path they truncated and filled the same temporary file at once, and what
	// landed under the rename was one writer's short document inside the other's
	// call, or the two torn together. Either parses to a state nobody asked for, or
	// to nothing at all — which hands the agent back to the environment at the next
	// start, silently.
	tmp, err := os.CreateTemp(dir, ".policy-*.json")
	if err != nil {
		return fmt.Errorf("write beside %s: %w", resolved, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	// 0600 explicitly: CreateTemp already makes it, but the file says which
	// categories this workstation stopped masking, and that is a description of
	// what its user handles.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s: %w", tmp.Name(), err)
	}
	if err := os.Rename(tmp.Name(), resolved); err != nil {
		return fmt.Errorf("move %s into place: %w", resolved, err)
	}
	return nil
}

// livePolicy is the agent's current state in the shape the file holds.
//
// Read from the detector rather than from the request that changed it, for the
// reason every surface redraws from the reply: the route is not a transaction, so a
// request whose category list was refused still applied its locales, and what has
// to survive the restart is what the agent *is*.
//
// Off is the intent — the whole switched-off set, including categories no loaded
// locale can emit — because that is what the policy remembers, and dropping the
// unreachable ones would silently switch a category back on the day its locale was
// loaded again.
func (s *Server) livePolicy() policyRequest {
	off := make([]string, 0, len(s.det.Disabled()))
	for _, cat := range s.det.Disabled() {
		off = append(off, string(cat))
	}

	return policyRequest{
		Off:          off,
		Substitution: s.det.Substitution().String(),
		Locales:      orEmpty(s.det.Locales()),
		SecretLevel:  s.det.SecretLevel().String(),
	}
}

// persistPolicy stores what the agent is now masking, so the next start finds it.
//
// A failure is logged and nothing else: the agent's job is masking, and refusing a
// change because it could not be written to disk would leave the person clicking
// with neither the change nor the old state. Warn rather than Info because the
// consequence is invisible until the restart that loses the setting.
func (s *Server) persistPolicy() {
	if s.policyFile == "" {
		return
	}
	if err := writePolicyFile(s.policyFile, s.livePolicy()); err != nil {
		s.log.Warn("this change will not survive a restart: the policy could not be stored",
			"file", s.policyFile, "error", err)
	}
}

// errPartialPolicy is a request missing part of the state. Told apart from a state
// the detector refused because the route answers it as a bad request, and because
// nothing was applied — so nothing is persisted, and at start-up the environment
// stays in charge exactly as it does for a file that would not parse.
var errPartialPolicy = errors.New("a policy replaces the whole state — send what the agent " +
	"currently reports on /healthz, with your change applied")

// applyPolicy puts a whole state onto a detector, and reports whether the
// substitution mode actually changed and whether the detector was touched at all.
//
// One applier for the two callers — the route, and the stored file at start-up —
// so an agent restarted into its saved state applies it exactly as the click did.
// Two appliers is how a category comes to be switched off through a menu and back
// on through a restart.
//
// Both spellings are parsed before anything is applied: a request naming an unknown
// mode must not leave a new locale selection behind it. What cannot be ordered away
// is the pair below — locales first, because it decides which categories exist to be
// switched off at all — and each refuses on its own, which is why the caller records
// what the detector *is* rather than what it was asked for.
//
// `applied` is what separates those two kinds of refusal, and the file depends on
// it: a request refused before the first change is one the agent was never told, so
// nothing may go to disk over it. The file's absence has to keep meaning "nobody
// has".
func applyPolicy(det *detector.Detector, req policyRequest) (modeChanged, applied bool, err error) {
	// The whole state, every time, and a request missing part of it is refused
	// rather than completed from what happens to be current.
	//
	// The alternative — "absent means unchanged" — cannot be written honestly here.
	// An empty locale list is a *valid* state, the one an agent starts in when
	// nothing is configured, so absence cannot mean "leave them alone" without
	// making "load none" unsayable. And a caller that sent only "off" would silently
	// wipe the locale selection, which is the request that turns an agent into one
	// masking almost nothing while reporting success.
	//
	// Here and not in the route, because the stored file is the same request read
	// back: guarded at the route alone, `{"off":["EMAIL"]}` got a 400 when sent and
	// was applied on restart — both parsers accept "" and SetLocales(nil) unloads
	// every locale — so a hand-edited file took the agent off its environment and
	// left it masking almost nothing.
	if req.Substitution == "" {
		return false, false, fmt.Errorf("%w, so \"substitution\" is required", errPartialPolicy)
	}
	if req.SecretLevel == "" {
		return false, false, fmt.Errorf("%w, so \"secret_level\" is required", errPartialPolicy)
	}

	mode, err := detector.ParseSubstitution(req.Substitution)
	if err != nil {
		return false, false, err
	}
	level, err := detector.ParseSecretLevel(req.SecretLevel)
	if err != nil {
		return false, false, err
	}

	before := det.Locales()
	if err := det.SetLocales(req.Locales); err != nil {
		return false, false, err
	}
	// From here the detector may have been changed, and if it was, whatever fails
	// below leaves a state somebody has to be able to read back after a restart.
	// Compared rather than assumed: SetLocales returning nil says the list was
	// valid, not that it differed, and every surface resends the whole state on
	// every click. Read as "applied", a request refused on its categories — a typo,
	// or a credential — created the file over locales identical to what was
	// loaded, and took the agent off its environment over a change of nothing.
	applied = !slices.Equal(before, det.Locales())

	cats := make([]pii.Category, 0, len(req.Off))
	for _, code := range req.Off {
		cats = append(cats, pii.Category(code))
	}
	// The detector is the one that refuses a credential, so nothing can route
	// around it — not this route, not a stored file somebody edited by hand.
	if err := det.Disable(cats); err != nil {
		return false, applied, err
	}

	// Read before the change so the caller can tell a real change from a request
	// that resent the mode it was already in — which every surface does on every
	// click, because this route replaces the whole state.
	modeChanged = det.Substitution() != mode
	det.SetSubstitution(mode)
	det.SetSecretLevel(level)
	return modeChanged, applied, nil
}
