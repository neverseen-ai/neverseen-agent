import { useStore } from "../app/store";
import { clock, licence, MODES, since } from "../copy";
import { tr } from "../i18n";
import { CommandLine, CopyButton } from "../ui/controls";
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
