import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { useStore } from "../../app/store";
import type { Snapshot } from "../../agent/types";
import { countryName, plural } from "../../copy";
import { tr } from "../../i18n";
import { Switch } from "../../ui/controls";
import { matches } from "./shared";

// One list, one row shape, filed by continent. What tells France from Germany is the
// gauge on each row — how many kinds of value the country's patterns find — rather
// than which block of the page the country was put in.
//
// A continent shows the countries that are loaded, and keeps the rest behind one
// line: forty rows nobody loaded stood between the reader and every switch below.
export function Countries({ snap, q }: { snap: Snapshot; q: string }) {
  const { change, busy } = useStore();
  const [unfolded, setUnfolded] = useState(false);
  const h = snap.health;
  const on = new Set(h.locales);
  const finds = h.locale_categories ?? {};
  const available = h.available_locales ?? [];
  const found = (code: string) => finds[code] ?? [];

  // The gauge is relative: full is the best-covered country the agent has, not "every
  // kind of value that country has". The figure beside it is the count, never a
  // percentage — the agent has no list of what a country could have, so there is
  // nothing to be a percentage of.
  const most = Math.max(1, ...available.map((c) => found(c).length));

  // One writer for a single switch and for a whole set.
  const load = (next: Iterable<string>, done: string, detail?: string) =>
    void change({ locales: [...new Set(next)].sort() }, done, { undo: true, detail });

  const toggle = (code: string) => {
    const adding = !on.has(code);
    const name = countryName(code);
    load(
      adding ? [...h.locales, code] : h.locales.filter((l) => l !== code),
      adding ? tr(`${name} is loaded`, `${name} : pays chargé`) : tr(`${name} is no longer loaded`, `${name} : pays retiré`),
      adding
        ? tr("What it finds is masked from the next request on.", "Ce qu'il trouve est masqué dès la prochaine requête.")
        : tr("What only it could find now goes out in clear.", "Ce que lui seul trouvait part désormais en clair."),
    );
  };

  const selectAll = (codes: string[]) => {
    const added = codes.filter((c) => !on.has(c)).length;
    load(
      [...h.locales, ...codes],
      tr(`${plural(added, "country", "countries")} loaded`, plural(added, "pays chargé", "pays chargés")),
      tr("What they find is masked from the next request on.", "Ce qu'ils trouvent est masqué dès la prochaine requête."),
    );
  };

  const selectNone = (codes: string[]) => {
    const drop = new Set(codes);
    const removed = codes.filter((c) => on.has(c)).length;
    load(
      h.locales.filter((l) => !drop.has(l)),
      tr(`${plural(removed, "country", "countries")} no longer loaded`, plural(removed, "pays retiré", "pays retirés")),
      tr("What only they could find now goes out in clear.", "Ce qu'eux seuls trouvaient part désormais en clair."),
    );
  };

  /** The two bulk buttons, over the countries shown. */
  const bulk = (codes: string[]) => {
    const loaded = codes.filter((c) => on.has(c)).length;
    return (
      <span className="btn-row">
        <button type="button" className="btn" disabled={busy || loaded === codes.length} onClick={() => selectAll(codes)}>
          {tr("Select all", "Tout sélectionner")}
        </button>
        <button type="button" className="btn" disabled={busy || loaded === 0} onClick={() => selectNone(codes)}>
          {tr("Select none", "Tout désélectionner")}
        </button>
      </span>
    );
  };

  const row = (code: string) => {
    const names = found(code);
    const name = countryName(code);
    const coverage = tr(
      `Finds ${plural(names.length, "kind of value", "kinds of value")}; the best-covered country finds ${most}`,
      `Trouve ${plural(names.length, "type de valeur", "types de valeurs")} ; le pays le mieux couvert en trouve ${most}`,
    );
    return (
      <div key={code} className={`country ${on.has(code) ? "" : "country-off"}`}>
        <span className="country-code">{code}</span>
        <div className="country-text">
          <span className="country-name">{name}</span>
          {/* Names, as well as the gauge: the lists overlap, so the figure alone cannot be added up. */}
          <span className="country-finds" title={names.join(", ")}>
            {names.length ? names.join(", ") : tr("Nothing of its own", "Rien qui lui soit propre")}
          </span>
        </div>
        <span className="gauge" role="img" aria-label={coverage} title={coverage}>
          <span className="gauge-bar">
            {Array.from({ length: most }, (_, i) => (
              <span key={i} className={i < names.length ? "gauge-on" : ""} />
            ))}
          </span>
          <span className="gauge-figure">{names.length}</span>
        </span>
        <Switch on={on.has(code)} disabled={busy} label={tr(`Load ${name}`, `Charger ${name}`)} onChange={() => toggle(code)} />
      </div>
    );
  };

  // Best-covered first: the countries that find the most come first. One list, since
  // the agent says nothing about where a country is.
  // Not by continent, as the design had it: forty-one of the forty-two are European,
  // so a section per continent would put a heading over almost the whole list.
  // TODO: the upgrade, once the catalogue reaches beyond Europe, is a continent per
  // locale in the registry, served beside locale_categories, and a section each here.
  const shownCodes = available
    .filter((c) => matches(q, c, countryName(c), ...found(c)))
    .sort((a, b) => found(b).length - found(a).length);
  // A search shows what it matched; otherwise the loaded countries, and the rest on request.
  const open = unfolded || !!q;
  const rest = shownCodes.filter((c) => !on.has(c));
  const rows = open ? shownCodes : shownCodes.filter((c) => on.has(c));

  // Intent no loaded country can act on: remembered, not drawn as a switch.
  const drawn = new Set((h.groups ?? []).flatMap((g) => g.categories.map((c) => c.code)));
  const remembered = (h.off ?? []).filter((c) => !drawn.has(c));

  return (
    <section aria-labelledby="countries-title" className="block">
      <h2 id="countries-title">{tr("Countries", "Pays")}</h2>
      <p className="lede">
        {tr(
          "Each country brings its own formats: social security number, telephone, number plate… Load the ones whose data you handle; the switches below depend on it. Secrets, and what depends on no country such as email addresses, are recognised either way.",
          "Chaque pays apporte ses propres formats : numéro de sécurité sociale, téléphone, plaque d'immatriculation… Chargez ceux dont vous manipulez des données ; les interrupteurs plus bas en dépendent. Les secrets, et ce qui ne dépend d'aucun pays comme les e-mails, sont reconnus dans tous les cas.",
        )}
      </p>
      {h.locales.length === 0 && (
        <p className="callout callout-bad">
          {tr(
            "No country is loaded. Postal addresses, telephone and national numbers go out as written.",
            "Aucun pays n'est chargé. Adresses postales, téléphones et numéros nationaux partent tels quels.",
          )}
        </p>
      )}
      <div className="panel countries">
        <header className="panel-head">
          <h3>
            {q
              ? tr(`${shownCodes.length} of ${available.length} countries shown`, `${plural(shownCodes.length, "pays affiché", "pays affichés")} sur ${available.length}`)
              : tr(`${on.size} of ${available.length} countries loaded`, `${plural(on.size, "pays chargé", "pays chargés")} sur ${available.length}`)}
          </h3>
          {shownCodes.length > 0 && bulk(shownCodes)}
        </header>
        {shownCodes.length === 0 && <p className="panel-empty">{tr(`No country matches “${q}”.`, `Aucun pays ne correspond à « ${q} ».`)}</p>}
        <div className="country-grid">{rows.map(row)}</div>
        {!q && rest.length > 0 && (
          <button type="button" className="fold" aria-expanded={open} onClick={() => setUnfolded((was) => !was)}>
            {open ? <ChevronDown size={14} aria-hidden /> : <ChevronRight size={14} aria-hidden />}
            {open
              ? tr("Show the loaded countries only", "N'afficher que les pays chargés")
              : tr(
                  `Show ${plural(rest.length, "more country", "more countries")} that ${rest.length === 1 ? "is" : "are"} not loaded`,
                  `Afficher ${plural(rest.length, "autre pays non chargé", "autres pays non chargés")}`,
                )}
          </button>
        )}
        <p className="panel-note gauge-legend">
          <span className="gauge-bar" aria-hidden>
            {Array.from({ length: most }, (_, i) => (
              <span key={i} className={i < Math.ceil(most / 2) ? "gauge-on" : ""} />
            ))}
          </span>
          {tr(
            `Coverage: how many kinds of data the agent can recognise for a country. A full gauge is the best-covered country, ${most} kinds — not everything that exists in that country.`,
            `Couverture : combien de sortes de données l'agent sait reconnaître pour un pays. Une jauge pleine correspond au pays le mieux couvert, ${most} sortes — pas à tout ce qui existe dans ce pays.`,
          )}
        </p>
      </div>
      <p className="fine">
        {tr(
          "Telephone numbers and postcodes exist in several countries: their switch stays on the page while any one of those is loaded.",
          "Le téléphone et le code postal existent dans plusieurs pays : leur interrupteur reste affiché tant que l'un de ces pays est chargé.",
        )}
        {remembered.length > 0 && (
          <>
            {" "}
            {tr("Switched off and remembered while no loaded country finds it:", "Désactivé et mémorisé tant qu'aucun pays chargé ne le trouve :")}{" "}
            {remembered.map((c, i) => (
              <span key={c}>
                {i > 0 && ", "}
                <code>{c}</code>
              </span>
            ))}
            . {tr("Loading a country that finds it will not switch it back on.", "Charger un pays qui le trouve ne le réactivera pas.")}
          </>
        )}
      </p>
    </section>
  );
}
