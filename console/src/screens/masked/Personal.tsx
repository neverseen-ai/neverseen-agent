import { useStore } from "../../app/store";
import { bulkSet, masterState, splitGroups, switchableOf, toggledSet } from "../../agent/decisions";
import type { HealthCategory, HealthGroup, Snapshot } from "../../agent/types";
import { tr } from "../../i18n";
import { Switch, TriCheck } from "../../ui/controls";
import { categoryMatches } from "./shared";

export function Personal({ snap, q }: { snap: Snapshot; q: string }) {
  const { change, busy } = useStore();
  const h = snap.health;
  const { personal } = splitGroups(h.groups);
  const cats = switchableOf(h.groups);
  const master = masterState(cats);

  const flip = (cat: HealthCategory) =>
    void change(
      { off: toggledSet(h.off ?? [], cat.code) },
      cat.off ? tr(`${cat.label} is masked again`, `${cat.label} : à nouveau masqué`) : tr(`${cat.label} is in clear`, `${cat.label} : en clair`),
      {
        undo: true,
        detail: cat.off
          ? undefined
          : tr("From the next request on, it goes to the provider as written.", "Dès la prochaine requête, la valeur part telle quelle chez le fournisseur."),
      },
    );

  const shownGroups = personal
    .map((g) => ({ g, cats: g.categories.filter((c) => categoryMatches(q, c) || categoryMatches(q, { code: g.code, label: g.label })) }))
    .filter((x) => x.cats.length > 0);

  return (
    <section aria-labelledby="personal-title" className="block">
      <h2 id="personal-title">{tr("What gets masked", "Ce qui est masqué")}</h2>
      <p className="lede">
        {tr(
          "One switch per kind of value. Switching one off sends those values to the provider in clear, from the next request on.",
          "Un interrupteur par type de valeur. En désactiver un envoie ces valeurs en clair au fournisseur, dès la prochaine requête.",
        )}
      </p>

      <TriCheck
        checked={master.checked}
        indeterminate={master.indeterminate}
        disabled={master.disabled || busy}
        onChange={() =>
          void change(
            { off: bulkSet(h.off ?? [], cats.map((c) => c.code), master.allOff) },
            master.allOff
              ? tr("Personal data is masked again", "Les données personnelles sont à nouveau masquées")
              : tr("No personal data is masked", "Aucune donnée personnelle n'est masquée"),
            { undo: true },
          )
        }
      >
        {master.total === 0
          ? tr("Nothing here can be switched", "Rien n'est modifiable ici")
          : master.off === 0
            ? tr("Everything here is masked", "Tout est masqué ici")
            : master.off === master.total
              ? tr("Nothing here is masked", "Rien n'est masqué ici")
              : tr(`${master.off} of ${master.total} in clear`, `${master.off} sur ${master.total} en clair`)}
      </TriCheck>

      {shownGroups.length === 0 && (
        <p className="panel-empty">{tr(`No personal-data category matches “${q}”.`, `Aucune catégorie de données personnelles ne correspond à « ${q} ».`)}</p>
      )}
      <div className="families">
        {shownGroups.map(({ g, cats }) => (
          <Family key={g.code} group={g} cats={cats} busy={busy} onFlip={flip} />
        ))}
      </div>
    </section>
  );
}

/** The shapes worth a second line: a notation that only says the label again is noise. */
function shapesOf(c: HealthCategory): string[] {
  const label = c.label.toLowerCase();
  const base = label.replace(/ \([a-z]{2}\)$/, "");
  return (c.notations ?? []).filter((n) => {
    const shape = n.toLowerCase();
    return !label.includes(shape) && !shape.startsWith(base);
  });
}

function Family({
  group,
  cats,
  busy,
  onFlip,
}: {
  group: HealthGroup;
  cats: HealthCategory[];
  busy: boolean;
  onFlip: (c: HealthCategory) => void;
}) {
  const off = group.categories.filter((c) => c.off).length;
  const total = group.categories.length;
  // A family that is wholly locked without being credentials is something this
  // deployment declared itself — not a credential, and not worded as one.
  const declared = group.categories.every((c) => c.locked);

  return (
    <section className="panel family" aria-labelledby={`fam-${group.code}`}>
      <header className="panel-head">
        <h3 id={`fam-${group.code}`}>{group.label}</h3>
        <span className={off ? "text-warn" : "muted"}>
          {off
            ? tr(`${off} of ${total} in clear`, `${off} sur ${total} en clair`)
            : tr(`${total} masked`, total < 2 ? `${total} masqué` : `${total} masqués`)}
        </span>
      </header>
      {declared && (
        <p className="panel-note">
          {tr(
            "Added by this machine's own configuration, so they cannot be switched off here.",
            "Ajoutés par la configuration de ce poste : ils ne se désactivent pas ici.",
          )}
        </p>
      )}
      <ul className="cats">
        {cats.map((c) => {
          const shapes = shapesOf(c);
          return (
            <li key={c.code} className={`cat ${c.off ? "cat-off" : ""}`}>
              <div className="cat-text">
                <span className="cat-label">{c.label}</span>
                {shapes.length > 0 && <span className="cat-shapes">{shapes.join(", ")}</span>}
              </div>
              {c.locked ? (
                <span className="muted small">{tr("Always", "Toujours")}</span>
              ) : (
                <Switch on={!c.off} disabled={busy} label={tr(`Mask ${c.label}`, `Masquer : ${c.label}`)} onChange={() => onFlip(c)} />
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
