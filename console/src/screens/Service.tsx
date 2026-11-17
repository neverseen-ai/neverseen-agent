import { useState } from "react";
import { agent } from "../agent/client";
import { enrolmentFrom } from "../agent/decisions";
import { useStore } from "../app/store";
import { clock, licence, MODES, since } from "../copy";
import { tr } from "../i18n";
import { CommandLine, CopyButton, Notice } from "../ui/controls";
import { Mark, type MarkState } from "../ui/Mark";

const ICONS: { state: MarkState; text: () => string }[] = [
  { state: "masking", text: () => tr("Masking everything it can recognise", "Masque tout ce qu'il sait reconnaître") },
  { state: "partial", text: () => tr("Masking, with categories in clear", "Masque, avec des catégories en clair") },
  { state: "unmasked", text: () => tr("Running, but masking almost nothing", "En marche, mais ne masque presque rien") },
  { state: "absent", text: () => tr("Not running: nothing between your tools and the provider", "Arrêté : rien entre vos outils et le fournisseur") },
];

// Where the installer's service writes the agent's output (internal/service).
const LOG = "~/.neverseen/agent.log";

// What the page can say about the agent's service: the version /healthz serves, when
// it started (GET /today, behind the key), the commands that act on the service, and
// what each file under ~/.neverseen is for. Starting, stopping and the diagnostics
// bundle are commands, not buttons: the agent serves no route that does either.
// TODO: whether the login service and the menu bar icon are installed is not said
// here. The upgrade is reporting what the installer's --status knows, behind the key.
export function Service() {
  const { snap, verdict } = useStore();
  if (!snap) return null;
  const running = verdict !== "stopped";

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>{tr("Service", "Service")}</h1>
          <p className="lede">
            {tr(
              "How the agent runs on this machine and where it keeps its files.",
              "Comment l'agent tourne sur cette machine, et où il range ses fichiers.",
            )}
          </p>
        </div>
      </header>

      <section className="panel" aria-labelledby="svc-title">
        <header className="panel-head">
          <h2 id="svc-title">{tr("Agent", "Agent")}</h2>
        </header>
        <dl className="facts facts-wide">
          <dt>{tr("State", "État")}</dt>
          <dd>
            {!running
              ? tr("Stopped", "Arrêté")
              : snap.today.agent
                ? tr(
                    `Running for ${since(snap.today.agent.started_at)}, since ${clock(snap.today.agent.started_at)}`,
                    `En marche depuis ${since(snap.today.agent.started_at)}, démarré le ${clock(snap.today.agent.started_at)}`,
                  )
                : tr("Running", "En marche")}
          </dd>
          <dt>{tr("Version", "Version")}</dt>
          <dd>{snap.health.version || tr("unknown while stopped", "inconnue à l'arrêt")}</dd>
          <dt>{tr("Log", "Journal")}</dt>
          <dd className="with-copy">
            <code>{LOG}</code> <CopyButton text={LOG} label={tr("Copy path", "Copier le chemin")} compact />
          </dd>
          <dt>{tr("State, in detail", "État détaillé")}</dt>
          <dd className="with-copy">
            <CommandLine text="neverseen status" />
          </dd>
          <dt>{tr("Restart", "Redémarrer")}</dt>
          <dd className="with-copy">
            <CommandLine text="neverseen service restart" />
          </dd>
        </dl>
      </section>

      <SupervisionPanel />

      <section className="block" aria-labelledby="files-title">
        <h2 id="files-title">{tr("Files in ~/.neverseen", "Fichiers dans ~/.neverseen")}</h2>
        <table className="table">
          <tbody>
            <tr>
              <td><code>policy.json</code></td>
              <td>
                {tr(
                  "The settings you change here. They take precedence over environment variables; delete the file to go back to those.",
                  "Les réglages que vous modifiez ici. Ils priment sur les variables d'environnement ; supprimez le fichier pour revenir à celles-ci.",
                )}
              </td>
            </tr>
            <tr>
              <td><code>control.key</code></td>
              <td>{tr("The key that gives control over the agent: this console and the extension use it. Readable by you alone.", "La clé qui donne la main sur l'agent : cette console et l'extension s'en servent. Lisible par vous seul.")}</td>
            </tr>
            <tr>
              <td><code>names.json</code>, <code>names.key</code></td>
              <td>
                {tr(
                  `The replacements already given out (${MODES.token.example}, ${MODES.fake.example}…), so a conversation finds them again after a restart. A fingerprint of each value is kept, never the value. Cleared a week after last use.`,
                  `Les remplacements déjà attribués (${MODES.token.example}, ${MODES.fake.example}…), pour qu'une conversation les retrouve après un redémarrage. Une empreinte de chaque valeur est gardée, jamais la valeur. Effacés une semaine après le dernier usage.`,
                )}
              </td>
            </tr>
            <tr>
              <td><code>agent.json</code>, <code>supervision.json</code></td>
              <td>
                {tr(
                  "Only on a supervised workstation: the identity the backend issued, which signs every report, and the backend's address. Never the enrolment token.",
                  "Seulement sur un poste supervisé : l'identité émise par le backend, qui signe chaque rapport, et l'adresse du backend. Jamais le jeton d'enrôlement.",
                )}
              </td>
            </tr>
            <tr>
              <td><code>agent.log</code></td>
              <td>
                {tr(
                  <>
                    The agent's log: numbers and category names, never your text, unless <code>-a</code> is on.
                  </>,
                  <>
                    Le journal de l'agent : des nombres et des noms de catégories, jamais vos textes, sauf si <code>-a</code> est
                    actif.
                  </>,
                )}
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <section className="block" aria-labelledby="icons-title">
        <h2 id="icons-title">{tr("What the menu bar icon says", "Ce que dit l'icône de la barre de menus")}</h2>
        <p className="lede">
          {tr(
            "On macOS, the number beside it is today's masked values.",
            "Sur macOS, le nombre à côté est celui des valeurs masquées aujourd'hui.",
          )}
        </p>
        <ul className="icon-legend">
          {ICONS.map((i) => (
            <li key={i.state} className={`legend-${i.state}`}>
              <Mark state={i.state} width={28} />
              <span>{i.text()}</span>
            </li>
          ))}
        </ul>
      </section>

      <p className="fine about">neverseen {snap.health.version}. {licence()}</p>
    </div>
  );
}

// Supervision is optional and the agent is whole without it, so this panel is the
// form until a backend has issued this workstation an identity, and only the state
// after: where it reports, as whom, and whether the reports arrive. Leaving a backend
// is not a button — the seat is the backend's to free, and a workstation that enrolled
// twice counts twice. Left out when the agent did not say (no key, an older build).
function SupervisionPanel() {
  const { snap, busy, working, act } = useStore();
  // Undefined until typed in, so the field follows what the agent offers.
  const [typed, setTyped] = useState<string>();
  const [token, setToken] = useState("");
  const s = snap?.today.agent?.supervision;
  if (!s) return null;

  const backend = typed ?? s.offered;
  const sent = enrolmentFrom(backend, token);
  const enrol = async () => {
    if (!sent) return;
    const done = await act(() => agent.enrol(sent.backend, sent.token), tr("This workstation is enrolled", "Ce poste est enrôlé"));
    // Kept after a refusal, for the typo to be corrected in; dropped once spent.
    if (done) setToken("");
  };

  return (
    <section className="panel" aria-labelledby="sup-title">
      <header className="panel-head">
        <h2 id="sup-title">{tr("Supervision", "Supervision")}</h2>
        <span className="muted">{s.agent_id ? tr("Enrolled", "Enrôlé") : tr("Not enrolled", "Non enrôlé")}</span>
      </header>
      {s.problem && (
        <div className="panel-note">
          <Notice tone="warn" title={tr("The last report did not go through", "Le dernier rapport n'est pas passé")}>
            {s.problem}
          </Notice>
        </div>
      )}
      {s.agent_id ? (
        <dl className="facts facts-wide">
          <dt>{tr("Backend", "Backend")}</dt>
          <dd><code>{s.backend}</code></dd>
          <dt>{tr("This workstation", "Ce poste")}</dt>
          <dd><code>{s.agent_id}</code></dd>
          <dt>{tr("Last report", "Dernier rapport")}</dt>
          <dd>
            {s.delivered_at
              ? tr(`${clock(s.delivered_at)}, ${since(s.delivered_at)} ago`, `${clock(s.delivered_at)}, il y a ${since(s.delivered_at)}`)
              : tr("None delivered yet", "Aucun livré pour l'instant")}
          </dd>
        </dl>
      ) : (
        <form
          className="enrol"
          onSubmit={(e) => {
            e.preventDefault();
            void enrol();
          }}
        >
          <p className="lede">
            {tr(
              "Optional: the agent masks the same without it. Enrolled, it reports every five minutes how many requests it relayed, how many values it masked in each category, how many tokens went to each model, and its configuration, with this machine's name and local addresses. Never a prompt, an answer or a masked value.",
              "Facultatif : l'agent masque de la même façon sans elle. Enrôlé, il rapporte toutes les cinq minutes le nombre de requêtes relayées, de valeurs masquées par catégorie, de tokens par modèle, et sa configuration, avec le nom et les adresses locales de cette machine. Jamais un prompt, une réponse ni une valeur masquée.",
            )}
          </p>
          <label>
            {tr("Backend address", "Adresse du backend")}
            <input
              className="input"
              type="url"
              value={backend}
              // Set by the environment: the agent would refuse any other.
              readOnly={!!s.backend}
              disabled={busy}
              onChange={(e) => setTyped(e.target.value)}
              spellCheck={false}
            />
          </label>
          <label>
            {tr("Enrolment token, as your backend's dashboard gave it. Single use: it is sent once and never stored.", "Jeton d'enrôlement, tel que le tableau de bord de votre backend l'a donné. À usage unique : il est envoyé une fois et jamais enregistré.")}
            <input
              className="input"
              type="password"
              value={token}
              disabled={busy}
              onChange={(e) => setToken(e.target.value)}
              autoComplete="off"
              spellCheck={false}
              placeholder="nse_…"
            />
          </label>
          <div className="btn-row">
            <button type="submit" className="btn btn-primary" disabled={busy || working || !sent}>
              {working ? tr("Enrolling…", "Enrôlement…") : tr("Enrol this workstation", "Enrôler ce poste")}
            </button>
          </div>
        </form>
      )}
    </section>
  );
}
