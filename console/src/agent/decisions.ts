// What a click means, kept out of what draws.
//
// The split internal/tray was built on, one storey up: a browser cannot be asserted on
// in CI any more than a menu bar can, so everything that *decides* lives here, where
// decisions.test.ts reaches it under `node --test`, and the screens only apply the
// results. Nothing here touches the DOM or says a sentence — a label is the screen's —
// and nothing here decides anything about a value: the agent is the one that masks,
// and these are set arithmetic over what it reported.
//
// Only type imports, which `node --test --experimental-strip-types` erases: a runtime
// import would pull the browser in with it.

import type { Health, HealthCategory, HealthGroup, Policy } from "./types";

/** The whole state to send with one part replaced: PUT /policy replaces rather than
 * patches, so a click carries the other parts unchanged or switches them off by
 * omission. `off` comes from the agent's whole intent, never from what is drawn. */
export function policyFrom(health: Health, change: Partial<Policy>): Policy {
  return {
    off: health.off ?? [],
    locales: health.locales ?? [],
    substitution: health.substitution,
    secret_level: health.secret_level,
    ...change,
  };
}

/** Adds a code to a set or removes it, sorted so two surfaces send the same set. */
export function toggledSet(set: string[], code: string): string[] {
  const next = new Set(set);
  if (next.has(code)) next.delete(code);
  else next.add(code);
  return [...next].sort();
}

/** The switched-off set after a whole section is switched at once. All or nothing, in
 * that direction: a partly-off section is switched fully off, never revived, and only
 * the codes drawn are touched. */
export function bulkSet(off: string[], drawn: string[], allOff: boolean): string[] {
  const next = new Set(off);
  for (const code of drawn) {
    if (allOff) next.delete(code);
    else next.add(code);
  }
  return [...next].sort();
}

/** Every category the whole-section control acts on: not credentials, not locked. */
export function switchableOf(groups: HealthGroup[] | undefined): HealthCategory[] {
  return (groups ?? []).filter((g) => !g.credentials).flatMap((g) => g.categories).filter((c) => !c.locked);
}

/** What the whole-section control shows — three states, not two. A partly-off section
 * is neither ticked nor clear, and `indeterminate` is what says so; `off` and `total`
 * are what the screen words it with. */
export function masterState(cats: HealthCategory[]) {
  const total = cats.length;
  const off = cats.filter((c) => c.off).length;
  return {
    checked: off === 0,
    indeterminate: off > 0 && off < total,
    disabled: total === 0,
    allOff: total > 0 && off === total,
    off,
    total,
  };
}

/** Files each family under one of the two headings, on the agent's own answer. */
export function splitGroups(groups: HealthGroup[] | undefined) {
  const personal: HealthGroup[] = [];
  const credentials: HealthGroup[] = [];
  for (const g of groups ?? []) (g.credentials ? credentials : personal).push(g);
  return { personal, credentials };
}

/** The categories in clear, by label, in catalogue order — names, not a count. */
export function inClear(health: Health): HealthCategory[] {
  return (health.groups ?? []).flatMap((g) => g.categories).filter((c) => c.off);
}

/** The control key this tab writes with: the one the page was served with, or the one
 * the tab kept from an earlier load. The page carries the key only when it was opened
 * with a one-time ticket, so a reload is served without it and the tab's own copy is
 * what keeps its controls working. A served key wins over a kept one: the agent may
 * hold a new key since the tab kept the old, and a tab writing with that would be
 * refused on every click. Neither is "", and the page then says where to open it from. */
export function keyFrom(served: string, kept: string): string {
  return served || kept;
}

// The settings page answered to a panel in its hash (#credentials), and two of its
// panels were merged into others before the console replaced it. A link outlives a
// layout, so each of those names lands on the tab holding that panel now.
const PANELS: Record<string, string> = {
  personal: "#/masked",
  countries: "#/masked",
  credentials: "#/masked?tab=credentials",
  secrets: "#/masked?tab=credentials",
  substitution: "#/masked?tab=substitution",
};

/** The console route an old settings-page hash names, or undefined for any other. */
export function routeForPanel(hash: string): string | undefined {
  const name = hash.replace(/^#/, "");
  return Object.hasOwn(PANELS, name) ? PANELS[name] : undefined;
}

/** Where the day's count starts: the agent's start when that was today, since the count
 * is held in memory and a restart starts it again — or undefined for midnight. */
export function countedSince(startedAt: string | undefined, now: Date): Date | undefined {
  if (!startedAt) return undefined;
  const start = new Date(startedAt);
  if (Number.isNaN(start.getTime()) || start > now) return undefined;
  return start.toDateString() === now.toDateString() ? start : undefined;
}

/** What POST /enrol is sent, or undefined while the form holds nothing to send. Both
 * trimmed: a token is pasted, and a paste carries the newline that followed it. */
export function enrolmentFrom(backend: string, token: string): { backend: string; token: string } | undefined {
  const sent = { backend: backend.trim(), token: token.trim() };
  return sent.backend && sent.token ? sent : undefined;
}

/** What Undo sends: the state now, with this one change reversed. Never the state from
 * before the change sent whole — another change may have landed since, a second toast's,
 * the menu bar's or `neverseen mask`'s, and the route replaces rather than patches, so
 * that one would be reverted too. */
export function undone(before: Policy, sent: Policy, now: Policy): Policy {
  const reversed = (was: string[], to: string[], current: string[]) => {
    const next = new Set(current);
    for (const code of to) if (!was.includes(code)) next.delete(code);
    for (const code of was) if (!to.includes(code)) next.add(code);
    return [...next].sort();
  };
  return {
    off: reversed(before.off, sent.off, now.off),
    locales: reversed(before.locales, sent.locales, now.locales),
    substitution: sent.substitution === before.substitution ? now.substitution : before.substitution,
    secret_level: sent.secret_level === before.secret_level ? now.secret_level : before.secret_level,
  };
}
