import { useEffect, useState } from "react";
import { CircleCheck, TriangleAlert } from "lucide-react";
import { useStore } from "../app/store";
import { agent } from "../agent/client";
import { SAMPLES } from "./samples";
import type { Rendering, TestResult } from "../agent/types";
import { tr, useLang } from "../i18n";
import { Empty } from "../ui/controls";
import { Mark } from "../ui/Mark";

// The one screen that shows real text, because it is the text somebody typed into it.
// Nothing here is sent anywhere, stored, or written to the session vault.
// The samples are named in English; their names are drawn in the language in use.
const SAMPLE_NAMES: Record<string, () => string> = {
  "Support ticket": () => tr("Support ticket", "Ticket de support"),
  "Customer export": () => tr("Customer export", "Export clients"),
  "UK and US customers": () => tr("UK and US customers", "Clients UK et US"),
  "API call": () => tr("API call", "Appel d'API"),
  ".env file": () => tr(".env file", "Fichier .env"),
  "Code review": () => tr("Code review", "Revue de code"),
  "Server log": () => tr("Server log", "Journal serveur"),
};

export function TryIt() {
  const { snap, verdict } = useStore();
  const [text, setText] = useState(SAMPLES[0].text);
  const [result, setResult] = useState<TestResult>();
  const [refused, setRefused] = useState<string>();
  // The findings' labels are translated as they arrive, so a change of language asks again.
  const language = useLang();

  useEffect(() => {
    if (verdict === "stopped") return;
    let live = true;
    // A refusal (a text over the agent's cap) clears the columns: left as they were,
    // they would show another text's rendering under this one. And it is said: an empty
    // result read as "nothing found" in a text the agent never read.
    const t = setTimeout(
      () =>
        void agent.test(text).then(
          (r) => {
            if (!live) return;
            setResult(r);
            setRefused(undefined);
          },
          (err: unknown) => {
            if (!live) return;
            setResult(undefined);
            setRefused((err as Error).message);
          },
        ),
      150,
    );
    return () => {
      live = false;
      clearTimeout(t);
    };
  }, [text, verdict, language, snap?.health.locales.join(), snap?.health.off?.join(), snap?.health.secret_level]);

  return (
    <div className="page page-wide">
      <header className="page-head">
        <div>
          <h1>{tr("Try it", "Essayer")}</h1>
          <p className="lede">
            {tr(
              "Paste a text to see what the provider would receive, with this agent's current settings, in both replacement modes. Nothing leaves this computer.",
              "Collez un texte pour voir ce que le fournisseur recevrait, avec les réglages actuels de cet agent, dans les deux modes de remplacement. Rien ne quitte ce poste.",
            )}
          </p>
        </div>
      </header>

      {verdict === "stopped" ? (
        <Empty icon={<Mark state="absent" width={56} />} title={tr("Nothing can be tried while the agent is stopped", "Rien ne peut être essayé tant que l'agent est arrêté")}>
          {tr(
            "The bench asks the agent, so it shows what the agent would really do. Start it from the Overview.",
            "Le banc d'essai interroge l'agent : il montre ce que l'agent ferait vraiment. Démarrez-le depuis la vue d'ensemble.",
          )}
        </Empty>
      ) : (
        <>
          <div className="bench-bar">
            <span className="muted">{tr("Start from", "Partir de")}</span>
            {SAMPLES.map((s) => (
              <button key={s.name} type="button" className={`btn btn-small ${text === s.text ? "btn-on" : ""}`} onClick={() => setText(s.text)}>
                {SAMPLE_NAMES[s.name]?.() ?? s.name}
              </button>
            ))}
            <button type="button" className="btn btn-small btn-quiet" onClick={() => setText("")}>
              {tr("Clear", "Effacer")}
            </button>
            <span className="spacer" />
            {result &&
              (result.roundTrips ? (
                <span className="text-ok small">
                  <CircleCheck size={14} aria-hidden /> {tr("Checked: putting the values back gives exactly this text", "Vérifié : remettre les valeurs redonne exactement ce texte")}
                </span>
              ) : (
                <span className="text-warn small">
                  <TriangleAlert size={14} aria-hidden /> {tr("The text already contains something that looks like a placeholder", "Le texte contient déjà quelque chose qui ressemble à une étiquette")}
                </span>
              ))}
          </div>

          <div className="bench">
            <section className="bench-col">
              <h2>{tr("As written", "Tel qu'écrit")}</h2>
              <textarea value={text} onChange={(e) => setText(e.target.value)} spellCheck={false} aria-label={tr("Text to try", "Texte à essayer")} />
              {/* A note under each column, so the three boxes end on one line. */}
              <p className="fine">{tr("Nothing here is sent, stored or remembered", "Rien ici n'est envoyé, stocké ni mémorisé")}</p>
            </section>
            <Column title={tr("Placeholders", "Étiquettes")} r={result?.token} note={tr("Put back in the answer", "Remises en clair dans la réponse")} />
            <Column
              title={tr("Stand-ins", "Valeurs fictives")}
              r={result?.fake}
              note={tr("Put back in the answer too; secrets keep a placeholder", "Remises en clair elles aussi ; les secrets gardent une étiquette")}
            />
          </div>

          <section className="block" aria-labelledby="found-title">
            <h2 id="found-title">{tr("Found", "Trouvé")}</h2>
            <Findings result={result} refused={refused} />
          </section>
        </>
      )}
    </div>
  );
}

function Findings({ result, refused }: { result?: TestResult; refused?: string }) {
  if (refused) {
    return (
      <p className="text-warn">
        <TriangleAlert size={14} aria-hidden /> {tr("The agent did not read this text:", "L'agent n'a pas lu ce texte :")} {refused}
      </p>
    );
  }
  if (!result) return null;
  if (result.findings.length === 0) {
    return (
      <p className="muted">
        {tr(
          "Nothing found in this text. If you expected something, it may belong to a country that is not loaded, or to a category that is switched off.",
          "Rien trouvé dans ce texte. Si vous attendiez quelque chose, cela relève peut-être d'un pays non chargé ou d'une catégorie désactivée.",
        )}
      </p>
    );
  }
  return (
    <>
      <table className="table">
        <thead>
          <tr>
            <th>{tr("Category", "Catégorie")}</th>
            <th>{tr("Recognised as", "Reconnu comme")}</th>
            <th>{tr("Value", "Valeur")}</th>
            <th className="num">{tr("Confidence", "Confiance")}</th>
          </tr>
        </thead>
        <tbody>
          {result.findings.map((f, i) => (
            <tr key={`${i}-${f.code}`}>
              <td>
                {f.label}
                {f.token_in_fake_mode && <span className="tag">{tr("placeholder in both modes", "étiquette dans les deux modes")}</span>}
              </td>
              <td className="muted">{f.notation}</td>
              <td>
                <code className="value">{f.value}</code>
              </td>
              <td className="num">{f.confidence}</td>
            </tr>
          ))}
        </tbody>
      </table>
      <p className="fine">
        {tr(
          "Confidence: how sure the agent is, out of 100. Where two readings overlap, a secret wins, then the surer one.",
          "Confiance : la certitude de l'agent, sur 100. Quand deux lectures se chevauchent, un secret l'emporte, puis la plus sûre.",
        )}
      </p>
    </>
  );
}

function Column({ title, r, note }: { title: string; r?: Rendering; note: string }) {
  return (
    <section className="bench-col">
      <h2>
        {title} <span className="muted">{r ? tr(`${r.count} replaced`, r.count < 2 ? `${r.count} remplacé` : `${r.count} remplacés`) : ""}</span>
      </h2>
      <pre className="bench-out" aria-label={title}>
        {r ? marked(r) : null}
      </pre>
      <p className="fine">{note}</p>
    </section>
  );
}

function marked(r: Rendering) {
  const out = [];
  let at = 0;
  for (const [i, m] of r.marks.entries()) {
    out.push(r.text.slice(at, m.start));
    out.push(
      <mark key={i} className={m.credential ? "rep rep-secret" : "rep"}>
        {r.text.slice(m.start, m.end)}
      </mark>,
    );
    at = m.end;
  }
  out.push(r.text.slice(at));
  return out;
}
