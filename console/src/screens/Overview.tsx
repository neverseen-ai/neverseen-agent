import { useState, type ReactNode } from "react";
import { Check, Info, OctagonAlert, TriangleAlert } from "lucide-react";
import { useStore, MARK_FOR } from "../app/store";
import { agent } from "../agent/client";
import { countedSince, inClear, toggledSet } from "../agent/decisions";
import type { ModelTokens, Snapshot } from "../agent/types";
import { LEVELS, MODES, countryName, listOf, plural } from "../copy";
import { compact, locale, num, tr } from "../i18n";
import { Empty } from "../ui/controls";
import { Mark, Pip } from "../ui/Mark";

export function Overview() {
  const { snap, verdict } = useStore();
  if (!snap) return null;
  return (
    <div className="page page-dash">
      <Verdict />
      <Exposure snap={snap} />
      {verdict === "stopped" ? (
        <Empty title={tr("Nothing is counted while the agent is stopped", "Rien n'est compté tant que l'agent est arrêté")}>
          {tr(
            "Counting resumes when it restarts.",
            "Le comptage reprend quand il redémarre.",
          )}
        </Empty>
      ) : (
        // Two columns of about the same height: the day on the left, the standing
        // facts and the traffic on the right, rather than a short card beside a tall one.
        <>
          <div className="overview-grid">
            <MaskedToday snap={snap} />
            <div className="overview-side">
              <Configuration snap={snap} />
              <Traffic snap={snap} />
            </div>
          </div>
          {snap.today.tokens && <Tokens snap={snap} tokens={snap.today.tokens} />}
        </>
      )}
    </div>
  );
}

function Verdict() {
  const { snap, verdict, change, go } = useStore();
  if (!snap) return null;
  const off = inClear(snap.health);
  const addr = snap.addr;

  const copy = {
    full: {
      title: tr("Masking", "Masquage actif"),
      body: tr(
        <>
          Everything the agent can recognise is replaced before it leaves this computer. It answers on <code>{addr}</code>.
        </>,
        <>
          Tout ce que l'agent sait reconnaître est remplacé avant de quitter ce poste. Il répond sur <code>{addr}</code>.
        </>,
      ),
      action: null,
    },
    partial: {
      title: tr(
        `Masking, with ${plural(off.length, "category", "categories")} in clear`,
        `Masquage, avec ${plural(off.length, "catégorie", "catégories")} en clair`,
      ),
      body: tr(
        "These go to the provider as written, from the next request on. Everything else is replaced.",
        "Elles partent telles quelles chez le fournisseur, dès la prochaine requête. Tout le reste est remplacé.",
      ),
      action: (
        <button
          type="button"
          className="btn btn-primary"
          onClick={() => void change({ off: [] }, tr("Everything is masked again", "Tout est à nouveau masqué"), { undo: true })}
        >
          {tr("Mask everything again", "Tout masquer à nouveau")}
        </button>
      ),
    },
    none: {
      title: tr("Answering, but masking almost nothing", "L'agent répond, mais ne masque presque rien"),
      body: tr(
        "No country is loaded, so only secrets and what depends on no country (email and IP addresses, IBANs…) are recognised. Postal addresses, telephone and national numbers go out as written.",
        "Aucun pays n'est chargé : seuls les secrets et ce qui ne dépend d'aucun pays (e-mails, adresses IP, IBAN…) sont reconnus. Adresses postales, téléphones et numéros nationaux partent tels quels.",
      ),
      action: (
        <button type="button" className="btn btn-primary" onClick={() => go("masked")}>
          {tr("Choose countries", "Choisir des pays")}
        </button>
      ),
    },
    stopped: {
      title: tr("Not running", "Arrêté"),
      body: tr(
        <>
          Nothing answers on <code>{addr}</code>. New terminals reach their provider directly, unmasked. That is deliberate: a
          stopped agent leaves them working rather than broken. A terminal opened while it was running, or a tool started with
          a one-run line, still points here and fails until it is back.
        </>,
        <>
          Rien ne répond sur <code>{addr}</code>. Les nouveaux terminaux joignent leur fournisseur directement, sans masquage.
          C'est voulu : un agent arrêté les laisse fonctionner plutôt que de les casser. Un terminal ouvert pendant qu'il
          tournait, ou un outil lancé avec une ligne à usage unique, vise encore l'agent et échoue jusqu'à son retour.
        </>,
      ),
      action: <code className="verdict-command">neverseen service restart</code>,
    },
  }[verdict];

  return (
    <section className={`verdict verdict-${verdict}`} aria-labelledby="verdict-title">
      <Mark state={MARK_FOR[verdict]} width={112} />
      <div className="verdict-text">
        <h1 id="verdict-title">{copy.title}</h1>
        <p>{copy.body}</p>
        {verdict === "partial" && (
          <ul className="clear-list" aria-label={tr("In clear", "En clair")}>
            {off.map((c) => (
              <li key={c.code}>
                <Pip clear /> {c.label}
              </li>
            ))}
          </ul>
        )}
        {copy.action && <div className="verdict-actions">{copy.action}</div>}
      </div>
    </section>
  );
}

/** When the day's figure starts counting, in the words the panel heads it with. */
function since(snap: Snapshot): string {
  const start = countedSince(snap.today.agent?.started_at, new Date());
  if (!start) return tr("since midnight, this machine's time", "depuis minuit, heure de cette machine");
  const time = start.toLocaleTimeString(locale(), { hour: "2-digit", minute: "2-digit" });
  return tr(`since ${time}, when the agent started`, `depuis ${time}, au démarrage de l'agent`);
}

// The day's figure as GET /today serves it, every family in it: the menu bar's own
// figure is the narrowed one, drawn beside the families it counts (MenuBarCount).
function MaskedToday({ snap }: { snap: Snapshot }) {
  const byFamily = snap.today.by_family;
  const count = byFamily ? Object.values(byFamily).reduce((a, b) => a + b, 0) : snap.today.count;
  return (
    <section className="panel" aria-labelledby="today-title">
      <header className="panel-head">
        <h2 id="today-title">{tr("Today", "Aujourd'hui")}</h2>
        <span className="muted">{since(snap)}</span>
      </header>
      <div className="panel-body">
        {count === undefined ? (
          // Left out rather than drawn as a zero nobody measured (proxy.QueryToday).
          <p className="today-missing">
            {snap.locked
              ? tr(
                  "The day's count is behind the control key, which this page was opened without.",
                  "Le compte du jour est protégé par la clé de contrôle, et cette page a été ouverte sans elle.",
                )
              : tr("This agent does not say how many values it masked today.", "Cet agent ne dit pas combien de valeurs il a masquées aujourd'hui.")}
          </p>
        ) : (
          <>
            <p className="today-figure">
              <span className="figure">{num(count)}</span>
              <span className="figure-label">
                {tr(count === 1 ? "value masked today" : "values masked today", count < 2 ? "valeur masquée aujourd'hui" : "valeurs masquées aujourd'hui")}
              </span>
            </p>
            <p className="fine">
              {tr("A value repeated within one conversation counts once.", "Une valeur répétée dans une même conversation ne compte qu'une fois.")}
            </p>
            {snap.today.hourly && <Hours snap={snap} hourly={snap.today.hourly} />}
            {byFamily && <MenuBarCount snap={snap} />}
            {snap.today.by_category && <ByCategory snap={snap} byCategory={snap.today.by_category} />}
          </>
        )}
      </div>
    </section>
  );
}

/** The day's figure by local hour. An hour still to come, or before the agent started,
 *  was not measured, so it is drawn as the flat line of one, never as a zero bar: the
 *  agent counts in memory, and a restart at noon says nothing about the morning. */
function Hours({ snap, hourly }: { snap: Snapshot; hourly: number[] }) {
  const now = new Date();
  const hour = now.getHours();
  const from = countedSince(snap.today.agent?.started_at, now)?.getHours() ?? 0;
  const peak = Math.max(1, ...hourly);
  return (
    <>
      <div className="hours" role="img" aria-label={tr("Values masked per hour today", "Valeurs masquées par heure aujourd'hui")}>
        {hourly.map((n, h) => {
          const measured = h >= from && h <= hour;
          return (
            <span
              key={h}
              className={`hour ${h === hour ? "hour-now" : ""} ${measured ? "" : "hour-unmeasured"}`}
              style={measured ? { height: `${Math.max(2, (n / peak) * 100)}%` } : undefined}
              title={`${String(h).padStart(2, "0")}:00${measured ? `, ${num(n)}` : ""}`}
            />
          );
        })}
      </div>
      <div className="hours-axis" aria-hidden>
        <span>00</span>
        <span>06</span>
        <span>12</span>
        <span>18</span>
        <span>24</span>
      </div>
    </>
  );
}

/** The model the agent sums every model under once the day has named seven. */
const OTHER_MODELS = "other";

/** A model's colour: the categorical slot of its place in the agent's list, which is the
 *  order the models were first seen today, so a model keeps its colour all day. The
 *  models past the seventh share the neutral grey. */
function seriesOf(m: ModelTokens, index: number): string {
  return m.model === OTHER_MODELS ? "var(--chart)" : `var(--series-${index + 1})`;
}

function modelLabel(m: ModelTokens): string {
  return m.model === OTHER_MODELS ? tr("Other models", "Autres modèles") : m.model;
}

function totalOf(m: ModelTokens): number {
  return m.day.input + m.day.output + (m.day.cache_write ?? 0) + (m.day.cache_read ?? 0);
}

/** The day's tokens by hour, one stacked column an hour and one segment a model, and
 *  the same day per model in a table — which carries every value the chart draws, for
 *  a reader who cannot tell two colours apart, and for three colours that are faint on
 *  the light surface. An hour before the agent started, or still to come, was not
 *  measured and is drawn as a flat line, as in the day's histogram. */
function Tokens({ snap, tokens }: { snap: Snapshot; tokens: ModelTokens[] }) {
  const [hovered, setHovered] = useState<number>();
  const now = new Date();
  const hour = now.getHours();
  const from = countedSince(snap.today.agent?.started_at, now)?.getHours() ?? 0;
  const byHour = Array.from({ length: 24 }, (_, h) => tokens.reduce((sum, m) => sum + (m.hourly[h] ?? 0), 0));
  const peak = Math.max(1, ...byHour);
  const day = tokens.reduce((sum, m) => sum + totalOf(m), 0);
  const clock = (h: number) => `${String(h).padStart(2, "0")}:00`;

  return (
    <section className="panel tokens" aria-labelledby="tokens-title">
      <header className="panel-head">
        <h2 id="tokens-title">{tr("Tokens", "Tokens")}</h2>
        <span className="muted">{since(snap)}</span>
      </header>
      <div className="panel-body">
        {tokens.length === 0 ? (
          <p className="muted">{tr("No answer has reported its tokens yet today.", "Aucune réponse n'a encore donné ses tokens aujourd'hui.")}</p>
        ) : (
          <>
            <p className="today-figure">
              <span className="figure">{compact(day)}</span>
              <span className="figure-label">{tr("tokens today, by hour and by model", "tokens aujourd'hui, par heure et par modèle")}</span>
            </p>
            <div className="token-chart">
              <div className="token-axis" aria-hidden>
                <span>{compact(peak)}</span>
                <span>0</span>
              </div>
              <div className="token-hours" role="group" aria-label={tr("Tokens per hour today", "Tokens par heure aujourd'hui")}>
                {byHour.map((total, h) => {
                  const measured = h >= from && h <= hour;
                  return (
                    <div
                      key={h}
                      className={`token-hour ${h < 4 ? "tip-start" : h > 19 ? "tip-end" : ""}`}
                      tabIndex={measured ? 0 : -1}
                      aria-label={measured ? `${clock(h)}, ${num(total)} tokens` : clock(h)}
                      onPointerEnter={() => setHovered(h)}
                      onPointerLeave={() => setHovered(undefined)}
                      onFocus={() => setHovered(h)}
                      onBlur={() => setHovered(undefined)}
                    >
                      {measured ? (
                        <div className="token-stack" style={{ height: `${(total / peak) * 100}%` }}>
                          {tokens.map(
                            (m, i) =>
                              (m.hourly[h] ?? 0) > 0 && (
                                <span key={m.model} style={{ flexGrow: m.hourly[h], background: seriesOf(m, i) }} />
                              ),
                          )}
                        </div>
                      ) : (
                        <div className="token-unmeasured" />
                      )}
                      {hovered === h && measured && total > 0 && (
                        <div className="token-tip" role="tooltip">
                          <div className="token-tip-head">
                            <strong>{num(total)}</strong> {tr(`tokens, ${clock(h)}–${clock(h + 1)}`, `tokens, ${clock(h)}–${clock(h + 1)}`)}
                          </div>
                          {tokens.map(
                            (m, i) =>
                              (m.hourly[h] ?? 0) > 0 && (
                                <div key={m.model} className="token-tip-row">
                                  <span className="token-key" style={{ background: seriesOf(m, i) }} />
                                  <strong>{num(m.hourly[h]!)}</strong> {modelLabel(m)}
                                </div>
                              ),
                          )}
                        </div>
                      )}
                    </div>
                  );
                })}
              </div>
              <span aria-hidden />
              <div className="hours-axis" aria-hidden>
                <span>00</span>
                <span>06</span>
                <span>12</span>
                <span>18</span>
                <span>24</span>
              </div>
            </div>
            <div className="table-scroll">
              <table className="token-table">
                <thead>
                  <tr>
                    <th scope="col">{tr("Model", "Modèle")}</th>
                    <th scope="col">{tr("Total", "Total")}</th>
                    <th scope="col">{tr("Input", "Entrée")}</th>
                    <th scope="col">{tr("Cache write", "Écriture cache")}</th>
                    <th scope="col">{tr("Cache read", "Lecture cache")}</th>
                    <th scope="col">{tr("Output", "Sortie")}</th>
                  </tr>
                </thead>
                <tbody>
                  {tokens.map((m, i) => (
                    <tr key={m.model}>
                      <th scope="row">
                        <span className="token-swatch" style={{ background: seriesOf(m, i) }} aria-hidden />
                        {modelLabel(m)}
                      </th>
                      <td>{num(totalOf(m))}</td>
                      <td>{num(m.day.input)}</td>
                      <td>{num(m.day.cache_write ?? 0)}</td>
                      <td>{num(m.day.cache_read ?? 0)}</td>
                      <td>{num(m.day.output)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
            <p className="fine">
              {tr(
                "As each provider reports them in its answer. A cache read is input served from the provider's cache, at a discount. Counts only: what they cost depends on each provider's prices.",
                "Tels que chaque fournisseur les donne dans sa réponse. Une lecture cache est une entrée servie depuis le cache du fournisseur, à prix réduit. Des nombres seulement : leur coût dépend des tarifs de chaque fournisseur.",
              )}
            </p>
          </>
        )}
      </div>
    </section>
  );
}

/** How many bars show before "Show all". */
const TOP = 8;

/** The day's figure per category, longest first. Minted counts, like the figure above, so
 *  the bars add up to it: a value quoted again in a conversation's history is not a bar's
 *  worth more. A category the agent no longer lists (a country since removed) keeps its
 *  code rather than being dropped from a sum it is part of. */
function ByCategory({ snap, byCategory }: { snap: Snapshot; byCategory: Record<string, number> }) {
  const [all, setAll] = useState(false);
  const labels = new Map((snap.health.groups ?? []).flatMap((g) => g.categories).map((c) => [c.code, c.label]));
  const entries = Object.entries(byCategory).sort((a, b) => b[1] - a[1]);
  const max = entries[0]?.[1] ?? 1;
  const shown = all ? entries : entries.slice(0, TOP);

  return (
    <>
      <h3 className="sub">{tr("By category", "Par catégorie")}</h3>
      {entries.length === 0 ? (
        <p className="muted">{tr("Nothing has been masked yet today.", "Rien n'a encore été masqué aujourd'hui.")}</p>
      ) : (
        <ol className="bars">
          {shown.map(([code, n]) => {
            const label = labels.get(code) ?? code;
            return (
              <li key={code}>
                <span className="bar-label" title={label}>
                  {label}
                </span>
                <span className="bar-track" aria-hidden>
                  <span className="bar" style={{ width: `${(n / max) * 100}%` }} />
                </span>
                <span className="bar-count">{num(n)}</span>
              </li>
            );
          })}
        </ol>
      )}
      {entries.length > TOP && (
        <button type="button" className="btn btn-link" onClick={() => setAll(!all)}>
          {all ? tr(`Show the top ${TOP}`, `Afficher les ${TOP} premières`) : tr(`Show all ${entries.length}`, `Afficher les ${entries.length}`)}
        </button>
      )}
    </>
  );
}

/** What the number beside the menu bar icon counts: the families the person leaves
 *  ticked, drawn from the ones the agent serves rather than a list of our own. A
 *  display choice, written through PUT /today and never through PUT /policy. */
function MenuBarCount({ snap }: { snap: Snapshot }) {
  const { verdict, act, busy } = useStore();
  const groups = snap.health.groups ?? [];
  const uncounted = snap.today.uncounted ?? [];
  const byFamily = snap.today.by_family ?? {};

  const toggle = (code: string, label: string) => {
    const counting = uncounted.includes(code);
    void act(
      () => agent.setUncounted(toggledSet(uncounted, code)),
      counting
        ? tr(`The menu bar counts ${label} again`, `Barre de menus : avec « ${label} »`)
        : tr(`The menu bar no longer counts ${label}`, `Barre de menus : sans « ${label} »`),
    );
  };

  return (
    <div className="tray-count">
      <div className="tray-count-head">
        <h3 className="sub">{tr("Counted in the menu bar", "Compté dans la barre de menus")}</h3>
        <span className="menubar" role="img" aria-label={tr("Menu bar preview", "Aperçu de la barre de menus")}>
          <Mark state={MARK_FOR[verdict]} width={18} />
          <span className="menubar-count">{num(snap.today.count ?? 0)}</span>
        </span>
      </div>
      <div className="picks" role="group" aria-label={tr("Families counted in the menu bar", "Familles comptées dans la barre de menus")}>
        {groups.map((g) => {
          const on = !uncounted.includes(g.code);
          return (
            <button key={g.code} type="button" className={`pick ${on ? "pick-on" : ""}`} aria-pressed={on} disabled={busy} onClick={() => toggle(g.code, g.label)}>
              {on && <Check size={12} aria-hidden />}
              {g.label}
              <span className="pick-n">{num(byFamily[g.code] ?? 0)}</span>
            </button>
          );
        })}
      </div>
      <p className="fine">
        {tr(
          "The number beside the icon, on macOS, counts the families ticked. This changes nothing about what is masked.",
          "Le nombre affiché à côté de l'icône, sur macOS, compte les familles cochées. Ce choix ne change rien au masquage.",
        )}
      </p>
    </div>
  );
}

// How the agent itself is exposed, as one folded list rather than a banner each: the
// title of a line is the warning, and the paragraph opens on request. Read from GET
// /today behind the key, the heartbeat State's own fields; nothing is drawn without it.
function Exposure({ snap }: { snap: Snapshot }) {
  const e = snap.today.agent;
  if (!e) return null;
  const items: { key: string; tone: "bad" | "warn" | "info"; title: string; body: ReactNode }[] = [];

  if (e.console)
    items.push({
      key: "console",
      tone: "bad",
      title: tr("Every value masked or put back is printed as it is (-a)", "Chaque valeur masquée ou remise en clair s'affiche telle quelle (-a)"),
      body: tr(
        <>
          Run as a service, the agent writes this output to <code>~/.neverseen/agent.log</code>: your prompts and tool calls
          stay on disk for as long as the flag is on. Keep <code>-a</code> for a one-off check.
        </>,
        <>
          Lancé comme service, l'agent écrit cette sortie dans <code>~/.neverseen/agent.log</code> : vos prompts et appels
          d'outils y restent sur disque tant que l'option est active. Réservez <code>-a</code> à une vérification ponctuelle.
        </>,
      ),
    });
  if (e.tracing)
    items.push({
      key: "tracing",
      tone: "bad",
      title: tr("Every exchange is written to traces/ (-v)", "Chaque échange est écrit dans traces/ (-v)"),
      body: tr(
        "Each request before and after masking, and the answer, in clear, one file per exchange. It is the only place this agent writes a value in clear to disk: read the files, then delete them.",
        "Chaque requête avant et après masquage, et la réponse, en clair, un fichier par échange. C'est le seul endroit où cet agent écrit une valeur en clair sur disque : lisez ces fichiers, puis supprimez-les.",
      ),
    });
  if (e.exposed)
    items.push({
      key: "exposed",
      tone: "warn",
      title: tr("Reachable beyond this workstation", "Joignable au-delà de ce poste"),
      body: tr(
        <>
          Other machines on the network can query the agent: read its state (<code>/healthz</code>) and have it mask a text
          (<code>/test</code>). Someone who guesses a conversation's name can get its replacements. Stay on{" "}
          <code>127.0.0.1</code> unless a container on this machine has to reach the agent.
        </>,
        <>
          D'autres machines du réseau peuvent interroger l'agent : lire son état (<code>/healthz</code>) et lui faire masquer
          un texte (<code>/test</code>). Quelqu'un qui devine le nom d'une conversation peut en obtenir les remplacements.
          Restez sur <code>127.0.0.1</code>, sauf si un conteneur de cette machine doit joindre l'agent.
        </>,
      ),
    });
  for (const code of e.rerouted ?? [])
    items.push({
      key: `r-${code}`,
      tone: "warn",
      title: tr(`${code} is routed somewhere other than the vendor`, `${code} est routé ailleurs que chez le fournisseur`),
      body: tr(
        <>
          That host receives the masked traffic and the credentials your tools send. Set by <code>NEVERSEEN_PROVIDERS</code>.
        </>,
        <>
          Cet hôte reçoit le trafic masqué et les identifiants envoyés par vos outils. Défini par{" "}
          <code>NEVERSEEN_PROVIDERS</code>.
        </>,
      ),
    });
  if (e.allowlisted)
    items.push({
      key: "allow",
      tone: "info",
      title: tr(
        `${plural(e.allowlisted, "value is", "values are")} never masked`,
        `${plural(e.allowlisted, "valeur n'est jamais masquée", "valeurs ne sont jamais masquées")}`,
      ),
      body: tr(
        <>
          Declared in <code>NEVERSEEN_PII_ALLOWLIST</code>. This page shows how many, never which.
        </>,
        <>
          Déclarées dans <code>NEVERSEEN_PII_ALLOWLIST</code>. Cette page montre combien, jamais lesquelles.
        </>,
      ),
    });

  if (!items.length) return null;
  const ICON = { bad: OctagonAlert, warn: TriangleAlert, info: Info };
  return (
    <section className="panel exposure" aria-labelledby="exposure-title">
      <header className="panel-head">
        <h2 id="exposure-title">
          {tr(
            `${plural(items.length, "thing to know", "things to know")} about how this agent runs`,
            `${plural(items.length, "point à connaître", "points à connaître")} sur la façon dont cet agent tourne`,
          )}
        </h2>
        {(e.console || e.tracing) && (
          <span className="muted small">
            {tr(
              "-a and -v are flags the agent was started with, and a service restart keeps them: stop it, and start it again without them.",
              "-a et -v sont des options passées au démarrage de l'agent, qu'un redémarrage du service conserve : arrêtez-le, puis relancez-le sans.",
            )}
          </span>
        )}
      </header>
      {items.map((i) => {
        const Icon = ICON[i.tone];
        return (
          <details key={i.key} className={`expo expo-${i.tone}`}>
            <summary>
              <Icon size={15} aria-hidden />
              <span>{i.title}</span>
            </summary>
            <p>{i.body}</p>
          </details>
        );
      })}
    </section>
  );
}

// The day's traffic, from GET /today behind the key. Left out without it.
function Traffic({ snap }: { snap: Snapshot }) {
  const t = snap.today.traffic;
  if (!t) return null;
  const u = t.upstream;
  const failed = (u.failed ?? 0) + (u.unreachable ?? 0);
  const clients = Object.entries(t.clients ?? {}).sort((a, b) => b[1] - a[1]);
  return (
    <section className="traffic" aria-label={tr("Traffic today", "Trafic du jour")}>
      <div className="stat">
        <span className="stat-value">{num(t.requests)}</span>
        <span className="stat-label">{tr("requests handled", "requêtes traitées")}</span>
      </div>
      <div className="stat">
        <span className="stat-value">{num(t.tools.calls)}</span>
        <span className="stat-label">{tr("tool calls asked for", "appels d'outils demandés")}</span>
        <span className="stat-note">{tr(`${t.tools.restored} used a value put back in clear`, `${t.tools.restored} avec une valeur remise en clair`)}</span>
      </div>
      <div className="stat">
        <span className="stat-value">{num(u.ok ?? 0)}</span>
        <span className="stat-label">{tr("answered by the provider", "réponses du fournisseur")}</span>
        <span className="stat-note">
          {tr(`${u.rate_limited ?? 0} rate limited, ${u.rejected ?? 0} rejected`, `${u.rate_limited ?? 0} limitées, ${u.rejected ?? 0} rejetées`)}
          {failed > 0 ? tr(`, ${failed} failed`, `, ${failed} en échec`) : ""}
        </span>
      </div>
      <div className="stat">
        <span className="stat-value">{num(t.refused)}</span>
        <span className="stat-label">{tr("requests refused", "requêtes refusées")}</span>
        <span className="stat-note">{tr("unreadable or too large, never sent", "illisibles ou trop grandes, jamais envoyées")}</span>
      </div>
      <div className="stat stat-wide">
        <span className="stat-label">{tr("Tools seen today", "Outils vus aujourd'hui")}</span>
        <ul className="clients">
          {clients.map(([name, n]) => (
            <li key={name}>
              <code>{name}</code> <span className="muted">{num(n)}</span>
            </li>
          ))}
          {clients.length === 0 && <li className="muted">{tr("None yet", "Aucun pour l'instant")}</li>}
        </ul>
      </div>
    </section>
  );
}

function Configuration({ snap }: { snap: Snapshot }) {
  const { go } = useStore();
  const h = snap.health;

  return (
    <section className="panel" aria-labelledby="config-title">
      <header className="panel-head">
        <h2 id="config-title">{tr("Running configuration", "Configuration en cours")}</h2>
        <button type="button" className="btn btn-link" onClick={() => go("masked")}>
          {tr("Change", "Modifier")}
        </button>
      </header>
      <dl className="facts">
        <dt>{tr("Countries", "Pays")}</dt>
        <dd>{h.locales.length ? listOf(h.locales.map(countryName)) : <span className="text-bad">{tr("None loaded", "Aucun chargé")}</span>}</dd>
        <dt>{tr("Replaced with", "Remplacé par")}</dt>
        <dd>
          {MODES[h.substitution]?.name ?? h.substitution} <code className="muted">{MODES[h.substitution]?.example}</code>
        </dd>
        <dt>{tr("Secret level", "Niveau de secret")}</dt>
        <dd>{LEVELS[h.secret_level]?.name ?? h.secret_level}</dd>
        <dt>{tr("Providers", "Fournisseurs")}</dt>
        <dd>
          <button type="button" className="btn btn-link" onClick={() => go("connect")}>
            {tr(`${plural(h.providers.length, "provider", "providers")} served`, plural(h.providers.length, "fournisseur servi", "fournisseurs servis"))}
          </button>
        </dd>
        <dt>{tr("Listening on", "Écoute sur")}</dt>
        <dd>
          <code>{snap.addr}</code>
        </dd>
        <dt>Version</dt>
        <dd>{h.version}</dd>
      </dl>
    </section>
  );
}
