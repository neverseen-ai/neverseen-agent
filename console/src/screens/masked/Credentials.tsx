import { useStore } from "../../app/store";
import { splitGroups } from "../../agent/decisions";
import type { Snapshot } from "../../agent/types";
import { LEVELS } from "../../copy";
import { tr } from "../../i18n";
import { categoryMatches, GRADED, LEVEL_RANK } from "./shared";

// Name and value are kept apart throughout: written as an assignment, a line would be
// a credential in this file to anything that scans it — this agent included.
//
// The strengths are pii.SecretStrength's, by character class: one class is weak, two
// are medium, three or more are strong. A separator is not a class.
const EXAMPLES: { name: string; value: string; strength: string; what: () => string }[] = [
  { name: "password", value: "soleil", strength: "weak", what: () => tr("a plain word", "un simple mot") },
  { name: "password", value: "soleil42", strength: "medium", what: () => tr("letters and digits", "des lettres et des chiffres") },
  { name: "token", value: "Kx7pQ2!vR9mB", strength: "strong", what: () => tr("a generated value", "une valeur générée") },
];
const token = (n: number) => `[${"SECRET"}_${n}]`; // assembled, so no surface expands it as a token

/** One line going out: what was written, and what the provider receives instead. */
function Before({ name, value, n }: { name: string; value: string; n: number }) {
  return (
    <span className="choice-demo" aria-hidden>
      <span className="demo-line">
        <span className="demo-name">{name} = </span>
        <span className="demo-clear">{value}</span>
        <span className="demo-name"> → </span>
        <span className="rep">{token(n)}</span>
      </span>
    </span>
  );
}

/** What a level does to the three lines: replaced from its own strength upwards. The value
 *  stays on screen either way, so the three cards are read against the same three values. */
function Demo({ level }: { level: string }) {
  return (
    <span className="choice-demo" aria-hidden>
      {EXAMPLES.map((e) => {
        const masked = LEVEL_RANK[e.strength] >= LEVEL_RANK[level];
        return (
          <span key={e.value} className={masked ? "demo-line demo-masked" : "demo-line"} title={e.what()}>
            <span className="demo-name">{e.name} = </span>
            <span className={masked ? "rep" : "demo-clear"}>{e.value}</span>
            <span className="demo-state">{masked ? tr("masked", "masqué") : tr("in clear", "en clair")}</span>
          </span>
        );
      })}
    </span>
  );
}

export function Credentials({ snap, q }: { snap: Snapshot; q: string }) {
  const { change, busy } = useStore();
  const h = snap.health;
  const { credentials } = splitGroups(h.groups);
  const graded = credentials.flatMap((g) => g.categories).find((c) => c.code === GRADED);
  const always = credentials
    .map((g) => ({ ...g, categories: g.categories.filter((c) => c.code !== GRADED) }))
    .filter((g) => g.categories.length > 0);
  const known = always.reduce((n, g) => n + g.categories.length, 0);

  return (
    <>
      <section aria-labelledby="how-title" className="block">
        <h2 id="how-title">{tr("How a secret is found", "Comment un secret est repéré")}</h2>
        <p className="lede">
          {tr(
            "Before a request leaves this computer, the agent reads it and replaces each secret with a placeholder. The provider never sees the value. When its answer repeats the placeholder, the agent puts the real value back, here. It recognises a secret in two ways:",
            "Avant qu'une requête quitte ce poste, l'agent la relit et remplace chaque secret par une étiquette. Le fournisseur ne voit jamais la valeur. Quand sa réponse reprend l'étiquette, l'agent remet la vraie valeur, ici. Il reconnaît un secret de deux façons :",
          )}
        </p>
        <div className="ways">
          <div className="way">
            <h3>{tr("By its shape: always masked", "À sa forme : toujours masqué")}</h3>
            <p>
              {tr(
                <>
                  Most services issue keys that always start the same way: <code>ghp_</code> at GitHub, <code>sk-ant-</code>{" "}
                  at Anthropic. The agent knows {known} formats, listed below, addresses carrying a password included.
                </>,
                <>
                  La plupart des services émettent des clés qui commencent toujours pareil : <code>ghp_</code> chez GitHub,{" "}
                  <code>sk-ant-</code> chez Anthropic. L'agent connaît {known} formats, listés plus bas, dont les adresses qui
                  contiennent un mot de passe.
                </>,
              )}
            </p>
            <Before name="GITHUB_TOKEN" value="ghp_…" n={1} />
          </div>
          <div className="way">
            <h3>{tr("By its name: depends on the strength", "À son nom : selon la force choisie")}</h3>
            <p>
              {tr(
                <>
                  A value with no known format is recognised by the name it is given: <code>password</code>,{" "}
                  <code>secret</code>, <code>token</code>, <code>api_key</code>… Nothing then says whether it is a real
                  password or an example, so the strength chosen below decides.
                </>,
                <>
                  Une valeur sans format connu est repérée par le nom qu'on lui donne : <code>password</code>,{" "}
                  <code>secret</code>, <code>token</code>, <code>api_key</code>… Rien ne dit alors si c'est un vrai mot de
                  passe ou un exemple : c'est la force choisie ci-dessous qui décide.
                </>,
              )}
            </p>
            <Before name="password" value="soleil42" n={2} />
          </div>
        </div>
      </section>

      <section aria-labelledby="level-title" className="block">
        <h2 id="level-title">{tr("Secret strength", "Force des secrets")}</h2>
        <p className="lede">
          {tr(
            "For a secret found by its name, the agent judges the value by how many kinds of character it mixes: lowercase, uppercase, digits, symbols. The higher the level, the less it masks.",
            "Pour un secret repéré à son nom, l'agent juge la valeur au nombre de sortes de caractères qu'elle mêle : minuscules, majuscules, chiffres, symboles. Plus le niveau est élevé, moins il masque.",
          )}
        </p>
        <div className="choices choices-levels" role="radiogroup" aria-labelledby="level-title">
          {(h.secret_levels ?? []).map((level) => {
            const live = level === h.secret_level;
            return (
              <label key={level} className={`choice ${live ? "choice-on" : ""}`}>
                <input
                  type="radio"
                  name="secret_level"
                  checked={live}
                  disabled={busy}
                  onChange={() =>
                    void change({ secret_level: level }, tr(`Secret level is ${LEVELS[level]?.name.toLowerCase() ?? level}`, `Niveau de secret : ${LEVELS[level]?.name.toLowerCase() ?? level}`), { undo: true })
                  }
                />
                <span className="choice-name">{LEVELS[level]?.name ?? level}</span>
                <span className="choice-why">{LEVELS[level]?.why ?? ""}</span>
                {level in LEVEL_RANK && <Demo level={level} />}
              </label>
            );
          })}
        </div>
        {graded && (
          <div className="graded">
            <span className="muted">{tr("This setting applies to:", "Ce réglage s'applique à :")}</span>
            {(graded.notations?.length ? graded.notations : [graded.label]).map((n) => (
              <code key={n} className="shape">
                {n}
              </code>
            ))}
          </div>
        )}
      </section>

      <section aria-labelledby="always-title" className="block">
        <h2 id="always-title">{tr("Always masked", "Toujours masqué")}</h2>
        <p className="lede">
          {tr(
            "The formats recognised by their shape. No setting lets them through: in clear, a key is a working key handed to someone else. Check that your own provider's is here.",
            "Les formats reconnus à leur forme. Aucun réglage ne les laisse passer : en clair, une clé reste une clé valide remise à un tiers. Vérifiez que celle de votre fournisseur y figure.",
          )}
        </p>
        {always.map((g) => {
          const cats = g.categories.filter((c) => categoryMatches(q, c));
          if (cats.length === 0) return null;
          return (
            <section key={g.code} className="panel" aria-labelledby={`cred-${g.code}`}>
              <header className="panel-head">
                <h3 id={`cred-${g.code}`}>{g.label}</h3>
                <span className="muted">
                  {q ? tr(`${cats.length} of ${g.categories.length}`, `${cats.length} sur ${g.categories.length}`) : g.categories.length}
                  {tr(", always masked", ", toujours masqués")}
                </span>
              </header>
              <ul className="shapes">
                {cats.map((c) => (
                  <li key={c.code}>
                    {(c.notations?.length ? c.notations : [c.label]).map((n) => (
                      <code key={n} className="shape">
                        {n}
                      </code>
                    ))}
                  </li>
                ))}
              </ul>
            </section>
          );
        })}
        {q && always.every((g) => !g.categories.some((c) => categoryMatches(q, c))) && (
          <p className="panel-empty">
            {tr(
              <>
                No known format matches “{q}”. A value with no known format is still found by its name, when it is assigned
                to <code>password</code>, <code>token</code>… and masked according to the strength above.
              </>,
              <>
                Aucun format connu ne correspond à « {q} ». Une valeur sans format connu reste repérée à son nom, quand elle
                est affectée à <code>password</code>, <code>token</code>… et masquée selon la force choisie plus haut.
              </>,
            )}
          </p>
        )}
      </section>
    </>
  );
}
