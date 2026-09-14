// The settings page's decisions, held to what they must do.
//
// Run with: make settings-test  (node --test, no dependency of any kind)
//
// It is the counterpart of internal/tray's split, and it exists for the reason that
// one does: a browser cannot be asserted on in CI, so what *decides* is kept where a
// test reaches it. Deleting plan.go and leaving these rules in a page nothing runs
// would have been the same feature with its tests removed.
//
// Each case is named for the failure it prevents, not for the function it calls.

import { test } from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";

const require = createRequire(import.meta.url);
const {
  policyFrom, toggledSet, bulkSet, switchableOf, masterState, splitGroups, panelFor,
} = require("./settings_decisions.js");

// A catalogue shaped like the one the agent serves: two switchable families, one of
// credentials, and one locked family that is not credentials.
const groups = () => [
  {
    code: "personal", label: "Personal details",
    categories: [
      { code: "EMAIL", label: "Email address" },
      { code: "PHONE", label: "Telephone", off: true },
    ],
  },
  {
    code: "declared", label: "Declared by this deployment",
    categories: [{ code: "CUSTOM", label: "Declared value", locked: true }],
  },
  {
    code: "secrets", label: "Secrets and keys", credentials: true,
    categories: [{ code: "SECRET_ANTHROPIC_KEY", label: "Anthropic key", locked: true }],
  },
];

test("a change carries the rest of the state, because the route replaces rather than patches", () => {
  const state = {
    off: ["SSN_US"], locales: ["fr"], substitution: "fake", secret_level: "strong",
  };

  const want = policyFrom(state, { substitution: "token" });

  assert.equal(want.substitution, "token");
  assert.deepEqual(want.off, ["SSN_US"], "the switched-off set was dropped by a change to the mode");
  assert.deepEqual(want.locales, ["fr"], "the locales were dropped");
  assert.equal(want.secret_level, "strong", "the secret level was dropped");
});

test("the switched-off set sent is the agent's whole intent, not what is on screen", () => {
  // SSN_US is switched off while `us` is not loaded, so no switch for it is drawn.
  // Rebuilt from the switches, this click would revive it — and the stored policy
  // file would make the loss permanent.
  const state = { off: ["SSN_US"], locales: ["fr"], substitution: "token", secret_level: "weak" };

  const want = policyFrom(state, { locales: ["fr", "gb"] });

  assert.ok(want.off.includes("SSN_US"),
    "a category no loaded locale can emit was revived by a click about something else");
});

test("toggling a category adds it or removes it, and the set is ordered", () => {
  assert.deepEqual(toggledSet(["PHONE"], "EMAIL"), ["EMAIL", "PHONE"]);
  assert.deepEqual(toggledSet(["EMAIL", "PHONE"], "EMAIL"), ["PHONE"]);
  assert.deepEqual(toggledSet([], "EMAIL"), ["EMAIL"]);
});

test("switching a partly-off section switches the rest off, rather than reviving it", () => {
  // The direction matters: reviving makes one click undo several deliberate ones.
  const off = bulkSet(["PHONE"], ["EMAIL", "PHONE"], false);
  assert.deepEqual(off, ["EMAIL", "PHONE"]);
});

test("switching a fully-off section back on clears only what is drawn", () => {
  // SSN_US is in the set and not in the drawn list: it must survive.
  const off = bulkSet(["EMAIL", "PHONE", "SSN_US"], ["EMAIL", "PHONE"], true);
  assert.deepEqual(off, ["SSN_US"]);
});

test("the whole-section control acts on nothing the agent would refuse", () => {
  const cats = switchableOf(groups());

  assert.deepEqual(cats.map(c => c.code), ["EMAIL", "PHONE"]);
  // A request naming a credential is refused whole, so including one would make the
  // click do nothing at all rather than switching off what it could.
  assert.ok(!cats.some(c => c.code.startsWith("SECRET_")), "a credential reached the control");
  assert.ok(!cats.some(c => c.code === "CUSTOM"), "what the deployment declared reached the control");
});

test("a partly-off section is neither checked nor unchecked", () => {
  const shown = masterState(switchableOf(groups()));

  assert.equal(shown.indeterminate, true, "a partly-off section drew as a plain state");
  assert.equal(shown.checked, false);
  assert.equal(shown.allOff, false, "a click here would have revived what is off");
  assert.equal(shown.label, "1 of 2 in clear");
});

test("a fully-masked and a fully-cleared section each say so, and the click reverses", () => {
  const all = [{ code: "EMAIL" }, { code: "PHONE" }];
  const masked = masterState(all);
  assert.equal(masked.checked, true);
  assert.equal(masked.indeterminate, false);
  assert.equal(masked.allOff, false);
  assert.equal(masked.label, "Everything here is masked");

  const cleared = masterState(all.map(c => ({ ...c, off: true })));
  assert.equal(cleared.checked, false);
  assert.equal(cleared.indeterminate, false);
  assert.equal(cleared.allOff, true, "a fully-cleared section would not switch back on");
  assert.equal(cleared.label, "Nothing here is masked");
});

test("a section with nothing switchable offers no control", () => {
  const shown = masterState([]);
  assert.equal(shown.disabled, true);
  assert.equal(shown.indeterminate, false);
});

test("a family is filed by what the agent says it is, never by whether it is locked", () => {
  const { personal, credentials } = splitGroups(groups());

  assert.deepEqual(credentials.map(g => g.code), ["secrets"]);
  // The case a split on `locked` gets wrong: every category in it is locked and none
  // of them is a credential, so it belongs with the personal data.
  assert.ok(personal.some(g => g.code === "declared"),
    "what the deployment declared was filed among the API keys");
  assert.deepEqual(personal.map(g => g.code), ["personal", "declared"]);
});

test("an empty catalogue files nothing and throws nothing", () => {
  assert.deepEqual(splitGroups(undefined), { personal: [], credentials: [] });
  assert.deepEqual(switchableOf(undefined), []);
});

test("a link to a panel that was merged into another lands on the one holding it", () => {
  const names = ["personal", "credentials", "substitution"];
  const aliases = { countries: "personal", secrets: "credentials" };

  assert.equal(panelFor("#countries", names, aliases), "personal");
  assert.equal(panelFor("#secrets", names, aliases), "credentials");
  assert.equal(panelFor("#credentials", names, aliases), "credentials");
  // No hash, and a name no panel answers to, both fall back rather than showing none.
  assert.equal(panelFor("", names, aliases), "personal");
  assert.equal(panelFor("#nothing-here", names, aliases), "personal");
});
