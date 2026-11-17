import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { agent } from "../agent/client";
import { policyFrom, routeForPanel, undone } from "../agent/decisions";
import { Refusal, type Policy, type Snapshot } from "../agent/types";
import { onLang, tr } from "../i18n";
import { useToast } from "../ui/overlays";
import type { MarkState } from "../ui/Mark";

// ---------------------------------------------------------------- routes

export type Screen = "overview" | "masked" | "try" | "connect" | "rtk" | "cloud" | "service";
export const SCREENS: Screen[] = ["overview", "masked", "try", "connect", "rtk", "cloud", "service"];

export interface Route {
  screen: Screen;
  params: URLSearchParams;
}

// The hash carries the screen and its sub-state (#/masked?tab=credentials&q=aws), so a
// link — or a screenshot taken from the command line — lands exactly where it points.
// A link to one of the old settings page's panels is rewritten to the tab holding it.
export function parseRoute(hash: string): Route {
  const old = routeForPanel(hash);
  if (old) {
    history.replaceState(null, "", old);
    hash = old;
  }
  const [path, query] = hash.replace(/^#\/?/, "").split("?");
  const screen = (SCREENS as string[]).includes(path) ? (path as Screen) : "overview";
  return { screen, params: new URLSearchParams(query ?? "") };
}

/** Takes a parameter out of the hash without a history entry. An explicit choice of
 * language or theme outlasts the link it was made on: left there, `?lang=` would beat
 * the choice again on the next reload. */
export function dropParam(name: string) {
  const { screen, params } = parseRoute(location.hash);
  if (!params.has(name)) return;
  params.delete(name);
  history.replaceState(null, "", routeHash(screen, Object.fromEntries(params)));
}

export function routeHash(screen: Screen, params?: Record<string, string | undefined>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params ?? {})) if (v) q.set(k, v);
  const s = q.toString();
  return `#/${screen}${s ? `?${s}` : ""}`;
}

// ---------------------------------------------------------------- the verdict

export type Verdict = "full" | "partial" | "none" | "stopped";

/** Level first, and "not running" apart from it: Status.Answering is not Status.Masking. */
export function verdictOf(snap: Snapshot | undefined): Verdict {
  if (!snap || !snap.answering) return "stopped";
  if (snap.health.locales.length === 0) return "none";
  return snap.health.masking === "partial" ? "partial" : snap.health.masking === "none" ? "none" : "full";
}

export const MARK_FOR: Record<Verdict, MarkState> = {
  full: "masking",
  partial: "partial",
  none: "unmasked",
  stopped: "absent",
};

// ---------------------------------------------------------------- the store

interface Store {
  snap: Snapshot | undefined;
  verdict: Verdict;
  /** Every control is drawn disabled: a page opened without the key can change nothing.
   *  Not while a change is in flight — a disabled control loses keyboard focus to the
   *  page, so a radio group could not be walked with the arrows — and a second change
   *  sent then is ignored instead (change, act). */
  busy: boolean;
  /** A change is in flight. */
  working: boolean;
  route: Route;
  go: (screen: Screen, params?: Record<string, string | undefined>) => void;
  refresh: () => Promise<void>;
  /** PUT /policy with one part changed, then redraw from the agent — on a refusal too. */
  change: (part: Partial<Policy>, done: string, opts?: { undo?: boolean; detail?: string }) => Promise<boolean>;
  /** Run an agent action, `working` while it runs, then a toast. */
  act: (run: () => Promise<unknown>, done: string, detail?: string) => Promise<boolean>;
}

const StoreContext = createContext<Store | undefined>(undefined);

export function useStore(): Store {
  const s = useContext(StoreContext);
  if (!s) throw new Error("useStore outside StoreProvider");
  return s;
}

export function StoreProvider({ children }: { children: ReactNode }) {
  const toast = useToast();
  const [snap, setSnap] = useState<Snapshot>();
  const [working, setWorking] = useState(false);
  const inFlight = useRef(false);
  const [route, setRoute] = useState(() => parseRoute(location.hash));

  // Only the latest asked is drawn. A poll sent before a PUT can answer after the
  // refresh that follows it, and drawn it would put the old state back on screen —
  // and the next click, built from it by policyFrom, would send that state back.
  const asked = useRef(0);
  // What an Undo is built on: the state when it is clicked, not when its toast was shown.
  const latest = useRef<Snapshot>();
  const refresh = useCallback(async () => {
    const n = ++asked.current;
    const next = await agent.snapshot();
    if (n === asked.current) {
      latest.current = next;
      setSnap(next);
    }
  }, []);

  useEffect(() => {
    void refresh();
    // The catalogue's names are served: a change of language asks for them again.
    const offLang = onLang(() => void refresh());
    // The menu bar polls every 5s; a console open beside it should not lag it.
    const poll = setInterval(() => void refresh(), 5000);
    const onHash = () => setRoute(parseRoute(location.hash));
    addEventListener("hashchange", onHash);
    return () => {
      offLang();
      clearInterval(poll);
      removeEventListener("hashchange", onHash);
    };
  }, [refresh]);

  const go = useCallback((screen: Screen, params?: Record<string, string | undefined>) => {
    const keep: Record<string, string | undefined> = {};
    // The theme and the language ride along, so a reload keeps them.
    const current = parseRoute(location.hash).params;
    for (const k of ["theme", "lang"]) keep[k] = current.get(k) ?? undefined;
    const hash = routeHash(screen, { ...params, ...keep });
    if (location.hash !== hash) history.pushState(null, "", hash);
    setRoute(parseRoute(hash));
    document.querySelector(".main")?.scrollTo({ top: 0 });
  }, []);

  const change = useCallback<Store["change"]>(
    async (part, done, opts) => {
      if (!snap || inFlight.current) return false;
      const before = policyFrom(snap.health, {});
      const sent = policyFrom(snap.health, part);
      inFlight.current = true;
      setWorking(true);
      try {
        await agent.setPolicy(sent);
        await refresh();
        toast({
          tone: "ok",
          text: done,
          detail: opts?.detail,
          action: opts?.undo
            ? {
                label: tr("Undo", "Annuler"),
                run: () => {
                  void agent
                    .setPolicy(undone(before, sent, policyFrom((latest.current ?? snap).health, {})))
                    .then(refresh)
                    .then(() => toast({ tone: "ok", text: tr("Put back as it was", "Remis comme avant") }))
                    .catch((err: unknown) => toast({ tone: "bad", text: tr("That change was refused", "Ce changement a été refusé"), detail: String((err as Error).message) }));
                },
              }
            : undefined,
        });
        return true;
      } catch (err) {
        // Redrawn from the agent rather than from the request that failed: the route
        // is not a transaction, and a page that put its own request back on screen
        // would show a state the agent is not in.
        await refresh();
        const status = err instanceof Refusal ? err.status : 0;
        toast({
          tone: "bad",
          text: status === 422 ? tr("The agent refused that change", "L'agent a refusé ce changement") : tr("That change did not go through", "Ce changement n'est pas passé"),
          detail: (err as Error).message,
        });
        return false;
      } finally {
        inFlight.current = false;
        setWorking(false);
      }
    },
    [snap, refresh, toast],
  );

  const act = useCallback<Store["act"]>(
    async (run, done, detail) => {
      if (inFlight.current) return false;
      inFlight.current = true;
      setWorking(true);
      try {
        await run();
        await refresh();
        toast({ tone: "ok", text: done, detail });
        return true;
      } catch (err) {
        await refresh();
        toast({ tone: "bad", text: tr("That did not go through", "L'action n'est pas passée"), detail: (err as Error).message });
        return false;
      } finally {
        inFlight.current = false;
        setWorking(false);
      }
    },
    [refresh, toast],
  );

  const value = useMemo<Store>(
    // A locked page draws every control disabled: nothing it sends would be accepted.
    () => ({ snap, verdict: verdictOf(snap), busy: !!snap?.locked, working, route, go, refresh, change, act }),
    [snap, working, route, go, refresh, change, act],
  );
  return <StoreContext.Provider value={value}>{children}</StoreContext.Provider>;
}
