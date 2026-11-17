// The console's one way to the agent: the routes every other surface uses. /healthz to
// draw, PUT /policy to change, GET /today for the day's count, POST /test to try a
// text. The page is served by the agent it talks to, so every call is same-origin and
// the Content Security Policy's `connect-src 'self'` is all it needs.

import { keyFrom } from "./decisions";
import { Refusal, type AgentClient, type Finding, type Health, type Policy, type Rendering, type Snapshot, type TestResult } from "./types";
import { served, tr } from "../i18n";

/** The header the control key travels in (internal/proxy/policy.go controlHeader). */
const CONTROL_HEADER = "X-Neverseen-Control";

/** Where the tab keeps its copy of the key — the same entry the settings page used. */
const KEPT_KEY = "neverseen-control";

// The key arrives in the page only when it was opened with a one-time ticket — the
// menu bar or `neverseen settings` — because a page typed by hand could be another
// account's (settings.go says why). The tab keeps its own copy so a reload still
// writes: sessionStorage belongs to this origin and this tab alone. Storage can be
// refused, and the page then works until it is reloaded.
//
// Under `npm run dev` the page is Vite's and no ticket put a key in it, so it asks for
// one, once per tab. Never in the build: the agent's own page is opened with a ticket.
let KEY = (() => {
  let servedKey = document.body.dataset.controlKey ?? "";
  if (import.meta.env.DEV && !servedKey && !sessionStorage.getItem(KEPT_KEY)) {
    servedKey = prompt("Development: paste the agent's control key (cat ~/.neverseen/control.key)")?.trim() ?? "";
  }
  let kept = "";
  try {
    if (servedKey) sessionStorage.setItem(KEPT_KEY, servedKey);
    else kept = sessionStorage.getItem(KEPT_KEY) ?? "";
  } catch {
    // Storage refused: nothing kept, nothing to read back.
  }
  // The ticket is spent. Left in the address bar it is a dead link to reload or share.
  if (new URLSearchParams(location.search).has("t")) {
    history.replaceState(null, "", location.pathname + location.hash);
  }
  return keyFrom(servedKey, kept);
})();

// A kept key the agent refuses is one it no longer holds — control.key was written again
// since the tab kept it. Dropped, so the page says where to open it from rather than
// failing every click with "not authorised".
function forgetKey() {
  KEY = "";
  try {
    sessionStorage.removeItem(KEPT_KEY);
  } catch {
    // Storage refused: nothing was kept.
  }
}

/** Under `npm run dev` the page is Vite's; the agent is the one on its usual port. */
const ADDR = import.meta.env.DEV ? "127.0.0.1:9787" : location.host;

function withKey(init: RequestInit = {}): RequestInit {
  const headers = new Headers(init.headers);
  if (KEY) headers.set(CONTROL_HEADER, KEY);
  return { ...init, headers, cache: "no-store" };
}

/** A refusal carries the agent's own sentence, which is what the toast shows. */
async function refusal(res: Response): Promise<Refusal> {
  const text = (await res.text()).trim().replace(/^neverseen:\s*/, "");
  return new Refusal(text || `${res.status} ${res.statusText}`, res.status);
}

// TODO: the catalogue's labels are served in English and translated here, from the
// table in i18n.ts, for a console drawn in French. The ceiling is that table: a label
// the agent gains and the table lacks is drawn in English. The upgrade is the agent
// serving its labels in the language asked for.
function translated(h: Health): Health {
  return {
    ...h,
    locale_categories: h.locale_categories && Object.fromEntries(Object.entries(h.locale_categories).map(([code, labels]) => [code, labels.map(served)])),
    groups: h.groups?.map((g) => ({
      ...g,
      label: served(g.label),
      categories: g.categories.map((c) => ({ ...c, label: served(c.label), notations: c.notations?.map(served) })),
    })),
  };
}

/** What the page draws while nothing answers: the agent that served it has stopped. */
const SILENT: Health = {
  status: "",
  version: "",
  locales: [],
  substitution: "",
  secret_level: "",
  providers: [],
  masking: "none",
};

export const agent: AgentClient = {
  async snapshot(): Promise<Snapshot> {
    let health: Health;
    try {
      const res = await fetch("/healthz", { cache: "no-store" });
      if (!res.ok) throw await refusal(res);
      health = translated((await res.json()) as Health);
    } catch {
      return { answering: false, addr: ADDR, health: SILENT, today: {}, locked: !KEY };
    }

    // Left out rather than drawn as a zero nobody measured (proxy.QueryToday): no key,
    // or an agent built before the route, and the page says nothing about the day.
    let today: Snapshot["today"] = {};
    if (KEY) {
      try {
        const res = await fetch("/today", withKey());
        if (res.status === 403) forgetKey();
        else if (res.ok) {
          const r = (await res.json()) as { masked: number } & Omit<Snapshot["today"], "count">;
          today = { count: r.masked, by_family: r.by_family, by_category: r.by_category, hourly: r.hourly, uncounted: r.uncounted, traffic: r.traffic, agent: r.agent, tokens: r.tokens };
        }
      } catch {
        // The count is optional; the rest of the page is not.
      }
    }
    return { answering: true, addr: ADDR, health, today, locked: !KEY };
  },

  async setPolicy(policy: Policy): Promise<Health> {
    if (!KEY) throw new Refusal(lockedSentence(), 403);
    const res = await fetch("/policy", withKey({ method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(policy) }));
    if (!res.ok) throw await refusal(res);
    return translated((await res.json()) as Health);
  },

  async setUncounted(families: string[]): Promise<void> {
    if (!KEY) throw new Refusal(lockedSentence(), 403);
    const res = await fetch("/today", withKey({ method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ uncounted: families }) }));
    if (!res.ok) throw await refusal(res);
  },

  async enrol(backend: string, token: string): Promise<void> {
    if (!KEY) throw new Refusal(lockedSentence(), 403);
    const res = await fetch("/enrol", withKey({ method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ backend, token }) }));
    if (!res.ok) throw await refusal(res);
  },

  async test(text: string): Promise<TestResult> {
    const res = await fetch("/test", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ text }), cache: "no-store" });
    if (!res.ok) throw await refusal(res);
    const r = (await res.json()) as WireTest;
    return {
      token: rendering(r.token),
      fake: rendering(r.fake),
      roundTrips: r.round_trips,
      findings: r.findings.map((f) => ({ ...f, label: served(f.label), notation: served(f.notation) })),
    };
  },
};

/** POST /test's answer as it travels (internal/proxy/testjson.go testReply). */
interface WireTest {
  token: WireColumn;
  fake: WireColumn;
  round_trips: boolean;
  findings: Finding[];
}

interface WireColumn {
  text: string;
  count: number;
  masks: { mask: string; credential: boolean }[];
}

/** The replacements a column carries, as spans to highlight. The agent sends the
 * strings rather than offsets — it counts bytes and this page counts UTF-16 units —
 * so they are found here. Where two overlap, the earlier wins. */
function rendering(c: WireColumn): Rendering {
  const found: Rendering["marks"] = [];
  for (const { mask, credential } of c.masks) {
    if (!mask) continue;
    for (let at = c.text.indexOf(mask); at >= 0; at = c.text.indexOf(mask, at + mask.length)) {
      found.push({ start: at, end: at + mask.length, credential });
    }
  }
  found.sort((a, b) => a.start - b.start || b.end - a.end);
  const marks: Rendering["marks"] = [];
  for (const m of found) if (!marks.length || m.start >= marks[marks.length - 1].end) marks.push(m);
  return { text: c.text, marks, count: c.count };
}

/** What a locked page says, wherever a control is tried. */
export function lockedSentence(): string {
  return tr(
    "This page was opened without its one-time link, so nothing on it can be changed. Open it from the menu bar (Settings…), or run: neverseen settings",
    "Cette page a été ouverte sans son lien à usage unique : rien n'y peut être modifié. Ouvrez-la depuis la barre de menus (Réglages…) ou lancez : neverseen settings",
  );
}
