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

import type { Health, HealthCategory, HealthGroup, Policy, RtkDay, RtkReading } from "./types";

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

/** What the Cloud screen and its entry in the sidebar say of the connection: the agent
 * did not say, it reports nowhere, its reports arrive, or they wait. Waiting is a
 * failed attempt or a queue — not a queue alone being empty, since a report that was
 * refused while nothing else waited is still not arriving. Never `absent` drawn as
 * `none`: a page without the key would offer a form to an agent already connected. */
export function cloudState(s: { agent_id?: string; problem?: string; queued?: number } | undefined): "absent" | "none" | "connected" | "waiting" {
  if (!s) return "absent";
  if (!s.agent_id) return "none";
  return s.problem || (s.queued ?? 0) > 1 ? "waiting" : "connected";
}

/** What the rtk screen shows. `found` is what GET /today says of the binary — absent
 * when the agent did not say, a page without the key or an agent that has stopped — and
 * `reading` is GET /rtk, absent while it is asked.
 *
 * `unknown` is never drawn as `absent`: the case for installing rtk, made to somebody
 * who has it, is a page that is wrong about the workstation it is on. And an rtk that
 * answered with no day is `fresh`, not a chart of ninety idle ones. */
export type RtkScreen = "unknown" | "absent" | "asking" | "problem" | "fresh" | "reading";

export function rtkScreen(found: boolean | undefined, reading: Pick<RtkReading, "installed" | "problem" | "days"> | undefined): RtkScreen {
  if (found === undefined) return "unknown";
  if (!found) return "absent";
  if (!reading) return "asking";
  // Gone between the lookup and the run.
  if (!reading.installed) return "absent";
  if (reading.problem) return "problem";
  return reading.days?.length ? "reading" : "fresh";
}

/** Whether the rtk screen asks again by itself. While rtk has recorded nothing, so the
 * first day shows without a reload; and while there is no reading — rtk answered with a
 * problem, or the agent did not answer (`failed`) — so a repair shows without leaving
 * the screen. Never once there is a reading: each asking runs rtk. */
export function rtkAsksAgain(state: RtkScreen, failed: boolean): boolean {
  return failed || state === "fresh" || state === "problem";
}

/** A day of the rtk screen: rtk's row, or zeros for a day it has none for. */
export interface RtkPeriodDay extends RtkDay {
  saved: number;
}

export interface RtkPeriod {
  days: RtkPeriodDay[];
  commands: number;
  input: number;
  output: number;
  saved: number;
  /** What the same number of days just before saved, absent when rtk's record does not
   * reach back over all of them: compared with half a period, any period is a rise. */
  before?: number;
}

/** The last `n` days ending on `today`, both UTC dates as rtk writes them, oldest
 * first. Every date is there, so a day nothing ran is a gap in the chart rather than
 * two days drawn side by side. A row whose output exceeds its input saved nothing. */
export function rtkPeriod(rows: RtkDay[], n: number, today: string): RtkPeriod {
  const byDate = new Map(rows.map((r) => [r.date, r]));
  const end = Date.parse(`${today}T00:00:00Z`);
  const dateAt = (back: number) => new Date(end - back * 86400000).toISOString().slice(0, 10);
  const dayAt = (back: number): RtkPeriodDay => {
    const date = dateAt(back);
    const row = byDate.get(date) ?? { date, commands: 0, input: 0, output: 0 };
    return { ...row, saved: Math.max(0, row.input - row.output) };
  };
  const sum = (days: RtkPeriodDay[], key: "commands" | "input" | "output" | "saved") => days.reduce((t, d) => t + d[key], 0);

  const days = Array.from({ length: n }, (_, i) => dayAt(n - 1 - i));
  const earliest = rows.reduce((min, r) => (r.date < min ? r.date : min), "9999");
  const covered = earliest <= dateAt(2 * n - 1);
  return {
    days,
    commands: sum(days, "commands"),
    input: sum(days, "input"),
    output: sum(days, "output"),
    saved: sum(days, "saved"),
    before: covered ? sum(Array.from({ length: n }, (_, i) => dayAt(2 * n - 1 - i)), "saved") : undefined,
  };
}

/** The share of each day's output rtk cut, as an SVG path: one point a day at the
 * middle of its column, on an axis running from `top` percent down to zero. A day
 * nothing ran lifts the pen — joined across, the line would draw a share for a day that
 * has none — and a share above `top` is drawn at the top rather than off the chart. */
export function sharePath(days: Pick<RtkPeriodDay, "commands" | "input" | "saved">[], top: number): string {
  const points: string[] = [];
  let pen = false;
  days.forEach((d, i) => {
    if (!d.commands || !d.input) {
      pen = false;
      return;
    }
    const cut = Math.min((100 * d.saved) / d.input, top);
    points.push(`${pen ? "L" : "M"}${i + 0.5} ${top - cut}`);
    pen = true;
  });
  return points.join(" ");
}

/** Which edge a day's tooltip hangs from, so that one near either end of the chart
 * stays inside it. */
export function tipSide(i: number, n: number): "tip-start" | "tip-end" | "" {
  if (i < n * 0.2) return "tip-start";
  if (i > n * 0.8) return "tip-end";
  return "";
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
