// The console's decisions, held to what they must do.
//
// Run with: make console-test (node --test, types stripped, no browser)
//
// It is the counterpart of internal/tray's split, and it exists for the reason that
// one does: a browser cannot be asserted on in CI, so what *decides* is kept where a
// test reaches it. These cases came over from the settings page the console replaced,
// with the failure each one prevents, and they hold the same rules.
//
// Each case is named for the failure it prevents, not for the function it calls.

import { test } from "node:test";
import assert from "node:assert/strict";
import { bulkSet, countedSince, keyFrom, masterState, policyFrom, routeForPanel, splitGroups, switchableOf, toggledSet, undone } from "./decisions.ts";
import type { Health, HealthGroup, Policy } from "./types.ts";

// A catalogue shaped like the one the agent serves: two switchable families, one of
// credentials, and one locked family that is not credentials.
const groups = (): HealthGroup[] => [
  {
    code: "personal",
    label: "Personal details",
    categories: [
      { code: "EMAIL", label: "Email address" },
      { code: "PHONE", label: "Telephone", off: true },
    ],
  },
  {
    code: "declared",
    label: "Declared by this deployment",
    categories: [{ code: "CUSTOM", label: "Declared value", locked: true }],
  },
  {
    code: "secrets",
    label: "Secrets and keys",
    credentials: true,
    categories: [{ code: "SECRET_ANTHROPIC_KEY", label: "Anthropic key", locked: true }],
  },
];

const health = (state: Partial<Health>): Health => ({
  status: "ok",
  version: "test",
  locales: [],
  substitution: "token",
  secret_level: "weak",
  providers: [],
  masking: "full",
  ...state,
});

test("a change carries the rest of the state, because the route replaces rather than patches", () => {
  const state = health({ off: ["SSN_US"], locales: ["fr"], substitution: "fake", secret_level: "strong" });

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
  const state = health({ off: ["SSN_US"], locales: ["fr"] });

  const want = policyFrom(state, { locales: ["fr", "gb"] });

  assert.ok(want.off.includes("SSN_US"), "a category no loaded locale can emit was revived by a click about something else");
});

test("toggling a category adds it or removes it, and the set is ordered", () => {
  assert.deepEqual(toggledSet(["PHONE"], "EMAIL"), ["EMAIL", "PHONE"]);
  assert.deepEqual(toggledSet(["EMAIL", "PHONE"], "EMAIL"), ["PHONE"]);
  assert.deepEqual(toggledSet([], "EMAIL"), ["EMAIL"]);
});

test("switching a partly-off section switches the rest off, rather than reviving it", () => {
  // The direction matters: reviving makes one click undo several deliberate ones.
  assert.deepEqual(bulkSet(["PHONE"], ["EMAIL", "PHONE"], false), ["EMAIL", "PHONE"]);
});

test("switching a fully-off section back on clears only what is drawn", () => {
  // SSN_US is in the set and not in the drawn list: it must survive.
  assert.deepEqual(bulkSet(["EMAIL", "PHONE", "SSN_US"], ["EMAIL", "PHONE"], true), ["SSN_US"]);
});

test("the whole-section control acts on nothing the agent would refuse", () => {
  const cats = switchableOf(groups());

  assert.deepEqual(
    cats.map((c) => c.code),
    ["EMAIL", "PHONE"],
  );
  // A request naming a credential is refused whole, so including one would make the
  // click do nothing at all rather than switching off what it could.
  assert.ok(!cats.some((c) => c.code.startsWith("SECRET_")), "a credential reached the control");
  assert.ok(!cats.some((c) => c.code === "CUSTOM"), "what the deployment declared reached the control");
});

test("a partly-off section is neither checked nor unchecked", () => {
  const shown = masterState(switchableOf(groups()));

  assert.equal(shown.indeterminate, true, "a partly-off section drew as a plain state");
  assert.equal(shown.checked, false);
  assert.equal(shown.allOff, false, "a click here would have revived what is off");
  assert.deepEqual([shown.off, shown.total], [1, 2], "the counts the screen words it with");
});

test("a fully-masked and a fully-cleared section each say so, and the click reverses", () => {
  const all = [
    { code: "EMAIL", label: "Email address" },
    { code: "PHONE", label: "Telephone" },
  ];
  const masked = masterState(all);
  assert.equal(masked.checked, true);
  assert.equal(masked.indeterminate, false);
  assert.equal(masked.allOff, false);
  assert.equal(masked.off, 0);

  const cleared = masterState(all.map((c) => ({ ...c, off: true })));
  assert.equal(cleared.checked, false);
  assert.equal(cleared.indeterminate, false);
  assert.equal(cleared.allOff, true, "a fully-cleared section would not switch back on");
  assert.equal(cleared.off, cleared.total);
});

test("a section with nothing switchable offers no control", () => {
  const shown = masterState([]);
  assert.equal(shown.disabled, true);
  assert.equal(shown.indeterminate, false);
});

test("a family is filed by what the agent says it is, never by whether it is locked", () => {
  const { personal, credentials } = splitGroups(groups());

  assert.deepEqual(
    credentials.map((g) => g.code),
    ["secrets"],
  );
  // The case a split on `locked` gets wrong: every category in it is locked and none
  // of them is a credential, so it belongs with the personal data.
  assert.ok(
    personal.some((g) => g.code === "declared"),
    "what the deployment declared was filed among the API keys",
  );
  assert.deepEqual(
    personal.map((g) => g.code),
    ["personal", "declared"],
  );
});

test("an empty catalogue files nothing and throws nothing", () => {
  assert.deepEqual(splitGroups(undefined), { personal: [], credentials: [] });
  assert.deepEqual(switchableOf(undefined), []);
});

test("a link to one of the settings page's panels lands on the tab holding it", () => {
  assert.equal(routeForPanel("#credentials"), "#/masked?tab=credentials");
  assert.equal(routeForPanel("#substitution"), "#/masked?tab=substitution");
  assert.equal(routeForPanel("#personal"), "#/masked");
  // Merged into others before the console, and still answered.
  assert.equal(routeForPanel("#secrets"), "#/masked?tab=credentials");
  assert.equal(routeForPanel("#countries"), "#/masked");
  // A console route and a name no panel had are left alone.
  assert.equal(routeForPanel("#/masked?tab=credentials"), undefined);
  assert.equal(routeForPanel("#constructor"), undefined, "a name every object carries is not a panel");
  assert.equal(routeForPanel(""), undefined);
});

test("a reload keeps writing with the key the tab was opened with", () => {
  assert.equal(keyFrom("", "kept"), "kept");
});

test("a key the agent just served wins over the one the tab kept", () => {
  // The agent was given a new key since the tab kept the old one: writing with the
  // kept one would be refused on every click.
  assert.equal(keyFrom("served", "kept"), "served");
});

test("a page opened with no ticket and nothing kept has no key", () => {
  assert.equal(keyFrom("", ""), "");
});

const state = (p: Partial<Policy> = {}): Policy => ({ off: [], locales: ["fr"], substitution: "token", secret_level: "weak", ...p });

test("undoing one change leaves a change made since standing", () => {
  // EMAIL switched off, then PHONE, then Undo on the first toast. The state from before
  // the first change, sent whole, put PHONE back as well.
  const before = state();
  const sent = state({ off: ["EMAIL"] });
  const now = state({ off: ["EMAIL", "PHONE"] });
  assert.deepEqual(undone(before, sent, now), state({ off: ["PHONE"] }));
});

test("undoing 'mask everything again' switches off only what it had switched on", () => {
  const before = state({ off: ["EMAIL", "PHONE"] });
  const sent = state({ off: [] });
  const now = state({ off: ["IBAN"] });
  assert.deepEqual(undone(before, sent, now).off, ["EMAIL", "IBAN", "PHONE"]);
});

test("undoing a level puts that level back and nothing else", () => {
  // A country loaded since, by another surface, stays loaded.
  const before = state({ secret_level: "weak" });
  const sent = state({ secret_level: "strong" });
  const now = state({ secret_level: "strong", locales: ["fr", "gb"] });
  assert.deepEqual(undone(before, sent, now), state({ secret_level: "weak", locales: ["fr", "gb"] }));
});

test("a count restarted today does not claim to run from midnight", () => {
  // Held in memory: an agent started at half past nine has counted since then.
  const now = new Date(2026, 10, 14, 15, 0);
  const started = new Date(2026, 10, 14, 9, 30);
  assert.equal(countedSince(started.toISOString(), now)?.getTime(), started.getTime());
  // Started on an earlier day, or never said: the count runs from midnight.
  assert.equal(countedSince(new Date(2026, 10, 13, 23, 0).toISOString(), now), undefined);
  assert.equal(countedSince("0001-01-01T00:00:00Z", now), undefined);
  assert.equal(countedSince(undefined, now), undefined);
  assert.equal(countedSince("not a date", now), undefined);
});
