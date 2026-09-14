// What a click on the settings page means, kept out of the part that draws.
//
// That separation is the rule internal/tray was built on and it survived the move to
// a page: a browser cannot be asserted on in CI any more than a menu bar can, so
// everything that *decides* lives where a test reaches it and settings.html only
// applies the results. plan.go held exactly this for the menu — the all-or-nothing
// rule for a family, the arithmetic around what may not be switched, which entry a
// click stood for — and deleting it without this file would have moved a hundred
// tested lines into JavaScript nothing runs.
//
// Inlined into the page rather than served at a path of its own, so the page stays
// one response with one inline script: no second route to reserve, and a Content
// Security Policy of `default-src 'none'` with nothing to exempt. The guarded export
// at the foot is what lets `node --test` load the same file the browser is given —
// `module` is undefined in a browser, so the line is inert there.
//
// Nothing here touches the DOM, and nothing here decides anything about a value: the
// agent is the one that masks, and these are set arithmetic over what it reported.

/**
 * policyFrom is the whole state to send, with one part replaced.
 *
 * PUT /policy replaces rather than patches, so a click has to carry the other three
 * parts unchanged or it switches them off by omission.
 *
 * `off` comes from the agent's whole intent — including categories no loaded locale
 * can emit — and never from the switches drawn on screen. Rebuilt from what is drawn,
 * a click made with `us` unloaded would silently revive a US category somebody had
 * switched off, and the stored policy file would make the loss permanent.
 */
function policyFrom(state, change) {
  return Object.assign({
    off: state.off || [],
    locales: state.locales || [],
    substitution: state.substitution,
    secret_level: state.secret_level,
  }, change);
}

/** toggledSet adds a code to a set or removes it, sorted so two surfaces send the
 * same set in the same order. */
function toggledSet(set, code) {
  const next = new Set(set);
  if (next.has(code)) {
    next.delete(code);
  } else {
    next.add(code);
  }
  return [...next].sort();
}

/**
 * bulkSet is the switched-off set after a whole section is switched at once.
 *
 * All or nothing, in that direction: a section with some of its switches off is
 * switched fully off, not revived. Reviving makes one click undo several deliberate
 * ones, which is the surprising direction to be surprised in.
 *
 * It touches only the codes it is given — what is drawn — so a category no loaded
 * locale can emit keeps whatever intent it had, for the same reason policyFrom reads
 * `state.off`.
 */
function bulkSet(off, drawn, allOff) {
  const next = new Set(off);
  for (const code of drawn) {
    if (allOff) {
      next.delete(code);
    } else {
      next.add(code);
    }
  }
  return [...next].sort();
}

/**
 * switchableOf is every category the whole-section control acts on: the ones that are
 * not credentials and not locked.
 *
 * Credentials are excluded because the agent refuses them, and a request naming one
 * is refused *whole* — the click would do nothing at all rather than switching off
 * what it could. What a deployment declared sensitive itself is excluded by the same
 * rule, for its own reason: it is the one category somebody authored on purpose.
 */
function switchableOf(groups) {
  return (groups || [])
    .filter((g) => !g.credentials)
    .flatMap((g) => g.categories || [])
    .filter((c) => !c.locked);
}

/**
 * masterState is what the whole-section control shows.
 *
 * Three states, not two: a section with some of its switches off is neither on nor
 * off, and `indeterminate` is what says so. The menu bar could never show this —
 * fyne.io/systray offers a tick and no tick and nothing between, which is why a
 * partly-off family had to spell the count into its own title there.
 */
function masterState(cats) {
  const total = cats.length;
  const off = cats.filter((c) => c.off).length;

  let label = off + " of " + total + " in clear";
  if (off === 0) {
    label = "Everything here is masked";
  } else if (off === total) {
    label = "Nothing here is masked";
  }

  return {
    checked: off === 0,
    indeterminate: off > 0 && off < total,
    disabled: total === 0,
    allOff: total > 0 && off === total,
    label: total === 0 ? "Nothing here can be switched" : label,
  };
}

/**
 * splitGroups files each family under one of the two headings.
 *
 * On the agent's own answer (`credentials`), never on a list of group codes here: a
 * second copy of the taxonomy would file a newly added family under the wrong
 * heading, quietly, since both headings draw the same switches. Deliberately not on
 * `locked` either — a family can be entirely locked without being credentials.
 */
function splitGroups(groups) {
  const personal = [];
  const credentials = [];
  for (const group of groups || []) {
    (group.credentials ? credentials : personal).push(group);
  }
  return { personal, credentials };
}

/**
 * panelFor resolves the panel a hash asks for.
 *
 * Aliases exist because a panel that was merged into another must not send a
 * bookmark back to the first tab: the hash is honoured precisely so a link outlives a
 * layout. An unknown name falls back to the first panel rather than to none.
 */
function panelFor(hash, names, aliases) {
  const asked = (hash || "").replace(/^#/, "");
  const wanted = (aliases || {})[asked] || asked;
  return names.includes(wanted) ? wanted : names[0];
}

// The one line that is not for the browser: `module` is undefined there, so this is
// inert, and `node --test` gets the same file the page is served.
if (typeof module !== "undefined") {
  module.exports = {
    policyFrom, toggledSet, bulkSet, switchableOf, masterState, splitGroups, panelFor,
  };
}
