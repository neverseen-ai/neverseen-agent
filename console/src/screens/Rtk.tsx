import { useEffect, useState } from "react";
import { agent, whereToOpen } from "../agent/client";
import { rtkAsksAgain, rtkPeriod, rtkScreen, sharePath, tipSide, type RtkPeriod, type RtkPeriodDay, type RtkScreen } from "../agent/decisions";
import type { RtkReading } from "../agent/types";
import { useStore } from "../app/store";
import { clock } from "../copy";
import { compact, locale, num, tr } from "../i18n";
import { Notice } from "../ui/controls";
import { RtkPitch } from "./RtkPitch";

// What rtk saved on this workstation, day by day. rtk is not this agent's: it is a
// separate tool that shortens the output of the commands a coding agent runs. Where the
// agent found none, the screen makes the case for it and says how to install it
// (RtkPitch); which of the two is drawn is decided in decisions.rtkScreen.
//
// Everything drawn is served by GET /rtk, read when the screen opens: rtk's daily
// rows, never its history, whose rows carry the command lines. Tokens are rtk's
// estimates and no price is derived from them, as for the day's tokens on Overview.

/** How often rtk is asked again while there is no reading to draw (rtkAsksAgain), so
 * the first day, or a repair, shows without a reload. Each asking runs rtk, which is
 * why it is not the store's 5s. */
const ASK_AGAIN_MS = 30000;

const PERIODS = [7, 30, 90];

/** The share axis stops here: rtk's savings sit well under it, and a 100% axis would
 * flatten the line into the bottom third. A day above it is drawn at the top. */
const SHARE_TOP = 60;

function share(saved: number, input: number): number {
  return input ? (100 * saved) / input : 0;
}

/** A share as the language in use writes it: 30.3%, 30,3 %. */
function pct(saved: number, input: number): string {
  return (share(saved, input) / 100).toLocaleString(locale(), { style: "percent", minimumFractionDigits: 1, maximumFractionDigits: 1 });
}

/** A UTC date as rtk writes it, in the reader's language. Formatted in UTC: in local
 * time, west of Greenwich, every day would be drawn as the one before. */
function day(date: string, weekday = false): string {
  return new Date(`${date}T00:00:00Z`).toLocaleDateString(locale(), { timeZone: "UTC", day: "numeric", month: "short", weekday: weekday ? "short" : undefined });
}

export function Rtk() {
  const { snap, route, go } = useStore();
  // What GET /today says of the binary; undefined when the agent did not say.
  const found = snap?.today.agent ? !!snap.today.agent.rtk : undefined;
  // Undefined while rtk is being asked; a string is the sentence saying why it was not.
  const [reading, setReading] = useState<RtkReading | string>();
  const [again, setAgain] = useState(0);

  // Asked when the screen opens, and again the moment the agent finds an rtk: the page
  // somebody is installing it from is this one.
  useEffect(() => {
    if (!found) {
      setReading(undefined);
      return;
    }
    let current = true;
    agent
      .rtk()
      .then((r) => current && setReading(r))
      .catch((err: unknown) => current && setReading((err as Error).message));
    return () => {
      current = false;
    };
  }, [found, again]);

  const read = typeof reading === "object" ? reading : undefined;
  const state = rtkScreen(found, read);

  const asksAgain = rtkAsksAgain(state, typeof reading === "string");
  useEffect(() => {
    if (!asksAgain) return;
    const timer = setInterval(() => setAgain((n) => n + 1), ASK_AGAIN_MS);
    return () => clearInterval(timer);
  }, [asksAgain]);

  if (typeof reading !== "string" && (state === "absent" || state === "fresh")) return <RtkPitch path={state === "fresh" ? read?.path : undefined} />;

  const asked = Number(route.params.get("days"));
  const n = PERIODS.includes(asked) ? asked : 30;
  const period = state === "reading" && read?.days ? rtkPeriod(read.days, n, new Date().toISOString().slice(0, 10)) : undefined;

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>{tr("rtk savings", "Économies rtk")}</h1>
          <p className="lede">
            {tr(
              "What rtk cut from the output of the commands your coding agents ran, before a model read it.",
              "Ce que rtk a retiré de la sortie des commandes lancées par vos agents de code, avant qu'un modèle ne la lise.",
            )}
          </p>
        </div>
        {period && (
          <div className="segmented" role="radiogroup" aria-label={tr("Period", "Période")}>
            {PERIODS.map((p) => (
              <button key={p} type="button" role="radio" aria-checked={p === n} onClick={() => go("rtk", { days: String(p) })}>
                {tr(`${p} days`, `${p} jours`)}
              </button>
            ))}
          </div>
        )}
      </header>

      {typeof reading === "string" ? (
        <Notice tone="info" title={tr("The agent did not say what rtk recorded", "L'agent n'a pas dit ce que rtk a enregistré")}>
          {reading}
        </Notice>
      ) : (
        <Unread state={state} problem={read?.problem} />
      )}

      {period && (
        <>
          <Summary period={period} n={n} />
          <Days period={period} n={n} />
          <Figures period={period} />
        </>
      )}

      {read?.installed && <Source read={read} />}
    </div>
  );
}

/** What stands where the charts would, while there is no reading to draw. */
function Unread({ state, problem }: { state: RtkScreen; problem?: string }) {
  if (state === "unknown") {
    // No key, or an agent that has stopped: nothing is claimed about this workstation.
    return (
      <Notice tone="info" title={tr("The agent did not say whether rtk is installed", "L'agent n'a pas dit si rtk est installé")}>
        {whereToOpen()}
      </Notice>
    );
  }
  if (state === "asking") return <p className="muted" aria-busy="true">{tr("Asking rtk…", "Interrogation de rtk…")}</p>;
  if (state !== "problem") return null;
  return (
    <Notice tone="warn" title={tr("rtk is installed and did not answer", "rtk est installé et n'a pas répondu")}>
      <p><code>{problem}</code></p>
      <p>
        {tr(
          "Nothing is shown rather than an earlier reading. Masking is unaffected: rtk is not on the path a request takes.",
          "Rien n'est affiché plutôt qu'une lecture antérieure. Le masquage n'est pas affecté : rtk n'est pas sur le chemin d'une requête.",
        )}
      </p>
    </Notice>
  );
}

function Summary({ period, n }: { period: RtkPeriod; n: number }) {
  const active = period.days.filter((d) => d.commands > 0).length;
  const best = period.days.reduce((a, d) => (d.saved > a.saved ? d : a), period.days[0]);
  const change = period.before ? Math.round((100 * (period.saved - period.before)) / period.before) : undefined;

  return (
    <div className="traffic traffic-4">
      <div className="stat">
        <span className="stat-value">{compact(period.saved)}</span>
        <span className="stat-label">{tr("tokens saved", "tokens économisés")}</span>
        {change !== undefined && (
          <span className="stat-note">
            {tr(`${change >= 0 ? "+" : "−"}${Math.abs(change)}% on the ${n} days before`, `${change >= 0 ? "+" : "−"}${Math.abs(change)} % sur les ${n} jours précédents`)}
          </span>
        )}
      </div>
      <div className="stat">
        <span className="stat-value">{pct(period.saved, period.input)}</span>
        <span className="stat-label">{tr("of the output cut", "de la sortie retirée")}</span>
        <span className="stat-note">{tr(`${compact(period.input)} before, ${compact(period.output)} after`, `${compact(period.input)} avant, ${compact(period.output)} après`)}</span>
      </div>
      <div className="stat">
        <span className="stat-value">{num(period.commands)}</span>
        <span className="stat-label">{tr("commands shortened", "commandes raccourcies")}</span>
        <span className="stat-note">{tr(`on ${active} of ${n} days`, `sur ${active} des ${n} jours`)}</span>
      </div>
      <div className="stat">
        <span className="stat-value">{compact(best.saved)}</span>
        <span className="stat-label">{tr("saved on the best day", "économisés le meilleur jour")}</span>
        {best.saved > 0 && <span className="stat-note">{day(best.date, true)}</span>}
      </div>
    </div>
  );
}

/** One stacked column a day — what rtk handed on, and above it what it cut — and under
 * them, on the same days, the share cut as a line. The table below carries every value. */
function Days({ period, n }: { period: RtkPeriod; n: number }) {
  const [hovered, setHovered] = useState<number>();
  const { days } = period;
  const peak = Math.max(1, ...days.map((d) => d.input));

  return (
    <section className="panel" aria-labelledby="rtk-days-title">
      <header className="panel-head">
        <h2 id="rtk-days-title">{tr("Tokens, day by day", "Tokens, jour par jour")}</h2>
        <span className="rtk-legend">
          <span><span className="token-swatch rtk-saved" />{tr("Saved by rtk", "Économisés par rtk")}</span>
          <span><span className="token-swatch rtk-kept" />{tr("Handed to the model", "Transmis au modèle")}</span>
        </span>
      </header>
      <div className="panel-body">
        <div className="token-chart rtk-chart">
          <div className="token-axis" aria-hidden>
            <span>{compact(peak)}</span>
            <span>0</span>
          </div>
          <div className={`token-hours rtk-days ${n > 30 ? "rtk-dense" : ""}`} role="group" aria-label={tr("Tokens per day", "Tokens par jour")}>
            {days.map((d, i) => (
              <div
                key={d.date}
                className={`token-hour ${tipSide(i, n)}`}
                tabIndex={d.commands ? 0 : -1}
                aria-label={d.commands ? tr(`${day(d.date)}, ${num(d.saved)} tokens saved`, `${day(d.date)}, ${num(d.saved)} tokens économisés`) : day(d.date)}
                onPointerEnter={() => setHovered(i)}
                onPointerLeave={() => setHovered(undefined)}
                onFocus={() => setHovered(i)}
                onBlur={() => setHovered(undefined)}
              >
                {d.commands ? (
                  <div className="token-stack" style={{ height: `${(d.input / peak) * 100}%` }}>
                    {d.output > 0 && <span className="rtk-kept" style={{ flexGrow: d.output }} />}
                    {d.saved > 0 && <span className="rtk-saved" style={{ flexGrow: d.saved }} />}
                  </div>
                ) : (
                  <div className="token-unmeasured" />
                )}
                {hovered === i && <Tip d={d} />}
              </div>
            ))}
          </div>
          <span aria-hidden />
          <div className="hours-axis" aria-hidden>
            <span>{day(days[0].date)}</span>
            <span>{day(days[Math.floor(n / 2)].date)}</span>
            <span>{day(days[n - 1].date)}</span>
          </div>
        </div>

        <h3 className="sub">{tr("Share of the output cut", "Part de la sortie retirée")}</h3>
        <div className="token-chart rtk-share">
          <div className="token-axis" aria-hidden>
            <span>{SHARE_TOP}%</span>
            <span>0%</span>
          </div>
          <svg viewBox={`0 0 ${n} ${SHARE_TOP}`} preserveAspectRatio="none" role="img" aria-label={tr("Share of the output cut, day by day", "Part de la sortie retirée, jour par jour")}>
            <path d={sharePath(days, SHARE_TOP)} />
          </svg>
        </div>
      </div>
    </section>
  );
}

function Tip({ d }: { d: RtkPeriodDay }) {
  return (
    <div className="token-tip" role="tooltip">
      <div className="token-tip-head">{day(d.date, true)}</div>
      {d.commands ? (
        <>
          <div className="token-tip-row">
            <span className="token-key rtk-saved" />
            <strong>{num(d.saved)}</strong> {tr("saved", "économisés")} ({pct(d.saved, d.input)})
          </div>
          <div className="token-tip-row">
            <span className="token-key rtk-kept" />
            <strong>{num(d.output)}</strong> {tr("handed to the model", "transmis au modèle")}
          </div>
          <div>
            <strong>{num(d.commands)}</strong> {tr("commands", "commandes")}
          </div>
        </>
      ) : (
        <div>{tr("No command went through rtk", "Aucune commande n'est passée par rtk")}</div>
      )}
    </div>
  );
}

function Figures({ period }: { period: RtkPeriod }) {
  return (
    <section className="panel" aria-labelledby="rtk-figures-title">
      <header className="panel-head">
        <h2 id="rtk-figures-title">{tr("The figures", "Les chiffres")}</h2>
        <span className="muted">{tr("Newest first", "Le plus récent d'abord")}</span>
      </header>
      <div className="table-scroll rtk-figures">
        <table className="token-table">
          <thead>
            <tr>
              <th scope="col">{tr("Day", "Jour")}</th>
              <th scope="col">{tr("Commands", "Commandes")}</th>
              <th scope="col">{tr("Before rtk", "Avant rtk")}</th>
              <th scope="col">{tr("After rtk", "Après rtk")}</th>
              <th scope="col">{tr("Saved", "Économisés")}</th>
              <th scope="col">{tr("Share cut", "Part retirée")}</th>
            </tr>
          </thead>
          <tbody>
            {[...period.days].reverse().map((d) => (
              <tr key={d.date} className={d.commands ? undefined : "rtk-idle"}>
                <th scope="row">{day(d.date, true)}</th>
                <td>{num(d.commands)}</td>
                <td>{num(d.input)}</td>
                <td>{num(d.output)}</td>
                <td>{num(d.saved)}</td>
                <td>
                  {d.commands ? (
                    <span className="rtk-share-cell">
                      <span className="bar-track">
                        <span className="bar rtk-saved" style={{ width: `${Math.min(100, (share(d.saved, d.input) / SHARE_TOP) * 100)}%` }} />
                      </span>
                      {pct(d.saved, d.input)}
                    </span>
                  ) : (
                    "—"
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </section>
  );
}

function Source({ read }: { read: RtkReading }) {
  return (
    <section className="panel" aria-labelledby="rtk-source-title">
      <header className="panel-head">
        <h2 id="rtk-source-title">{tr("Where these figures come from", "D'où viennent ces chiffres")}</h2>
        {read.read_at && <span className="muted">{tr(`Read ${clock(read.read_at)}`, `Lu le ${clock(read.read_at)}`)}</span>}
      </header>
      <dl className="facts facts-wide">
        <dt>{tr("Read from", "Lu depuis")}</dt>
        <dd>
          <code>rtk gain --daily --format json</code>
          <span className="origin">
            {tr("One row a day. Never rtk's history, whose rows carry the command lines.", "Une ligne par jour. Jamais l'historique de rtk, dont les lignes contiennent les commandes.")}
          </span>
        </dd>
        <dt>{tr("Binary", "Binaire")}</dt>
        <dd><code>{read.path}</code></dd>
        <dt>{tr("What a token is here", "Ce qu'est un token ici")}</dt>
        <dd>
          {tr("rtk's estimate", "Une estimation de rtk")}
          <span className="origin">
            {tr(
              "It measures the text it shortened, not what a provider billed. No price is derived from it.",
              "rtk mesure le texte qu'il a raccourci, pas ce qu'un fournisseur a facturé. Aucun prix n'en est déduit.",
            )}
          </span>
        </dd>
        <dt>{tr("What is counted", "Ce qui est compté")}</dt>
        <dd>
          {tr("The whole workstation", "Tout le poste")}
          <span className="origin">
            {tr(
              "Every command rtk shortened, whether or not that conversation went through this agent.",
              "Chaque commande raccourcie par rtk, que la conversation soit passée ou non par cet agent.",
            )}
          </span>
        </dd>
        <dt>{tr("What a day is", "Ce qu'est un jour")}</dt>
        <dd>
          {tr("Midnight to midnight, UTC", "De minuit à minuit, UTC")}
          <span className="origin">
            {tr(
              "rtk's own cut, and it keeps ninety of them: what is older is purged.",
              "C'est le découpage de rtk, qui en garde quatre-vingt-dix : ce qui est plus ancien est purgé.",
            )}
          </span>
        </dd>
      </dl>
    </section>
  );
}
