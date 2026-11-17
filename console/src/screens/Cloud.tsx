import { useState, type ReactNode } from "react";
import { Check, X } from "lucide-react";
import { agent, whereToOpen } from "../agent/client";
import { cloudState, enrolmentFrom } from "../agent/decisions";
import type { Supervision } from "../agent/types";
import { useStore } from "../app/store";
import { clock, plural, since } from "../copy";
import { tr } from "../i18n";
import { Notice } from "../ui/controls";
import { Dialog } from "../ui/overlays";

// The connection to Neverseen Cloud, or to a backend a company hosts: optional, and
// the agent is whole without it. The screen is the form until a backend has issued
// this workstation an identity, and the state after — where it reports, as whom, and
// whether the reports arrive. Leaving a backend is not a button: the seat is the
// backend's to free, and a workstation that connected twice counts twice.
//
// Everything drawn is served (GET /today, GET /report); the two lists of what a
// report carries are prose, and the document behind "See what the next report holds"
// is what makes them checkable.
export function Cloud() {
  const { snap } = useStore();
  if (!snap) return null;
  const s = snap.today.agent?.supervision;

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>Neverseen Cloud</h1>
          <p className="lede">
            {tr(
              "Optional. The agent masks the same without it: connected, this workstation appears in your team's dashboard, with counts and never content.",
              "Facultatif. L'agent masque de la même façon sans lui : connecté, ce poste apparaît dans le tableau de bord de votre équipe, avec des nombres et jamais de contenu.",
            )}
          </p>
        </div>
      </header>

      {s ? (
        <Connection s={s} />
      ) : (
        // No key, a stopped agent or an older build: the agent did not say, so no
        // form is drawn that could not be sent.
        <Notice tone="info" title={tr("The agent did not say whether it is connected", "L'agent n'a pas dit s'il est connecté")}>
          {whereToOpen()}
        </Notice>
      )}

      <Carries />

      <section className="block" aria-labelledby="cloud-files-title">
        <h2 id="cloud-files-title">{tr("What the connection keeps in ~/.neverseen", "Ce que la connexion garde dans ~/.neverseen")}</h2>
        <table className="table">
          <tbody>
            <tr>
              <th scope="row"><code>agent.json</code></th>
              <td>
                {tr(
                  "The identity issued to this workstation, which signs every report. Readable by you alone.",
                  "L'identité émise pour ce poste, qui signe chaque rapport. Lisible par vous seul.",
                )}
              </td>
            </tr>
            <tr>
              <th scope="row"><code>supervision.json</code></th>
              <td>{tr("The address the agent reports to. Never the enrolment token.", "L'adresse à laquelle l'agent rapporte. Jamais le jeton d'enrôlement.")}</td>
            </tr>
            <tr>
              <th scope="row"><code>buffer.json</code></th>
              <td>
                {tr(
                  "The reports not yet delivered: counts only, kept seven days at most.",
                  "Les rapports pas encore livrés : des nombres seulement, gardés sept jours au plus.",
                )}
              </td>
            </tr>
          </tbody>
        </table>
      </section>
    </div>
  );
}

function Connection({ s }: { s: Supervision }) {
  // The agent's own sentence for the last refusal, kept beside the form it is about:
  // a toast is gone before the dashboard has been opened for another token.
  const [refused, setRefused] = useState("");
  const state = cloudState(s);

  return (
    <section className="panel" aria-labelledby="conn-title">
      <header className="panel-head">
        <h2 id="conn-title">{tr("Connection", "Connexion")}</h2>
        {state === "none" && <span className="muted">{tr("Not connected", "Non connecté")}</span>}
        {state === "connected" && (
          <span className="text-ok small">
            <Check size={14} aria-hidden /> {tr("Connected", "Connecté")}
          </span>
        )}
        {state === "waiting" && <span className="text-warn small">{tr("Connected, reports waiting", "Connecté, rapports en attente")}</span>}
      </header>

      {refused && (
        <div className="panel-note">
          <Notice tone="bad" title={tr("This workstation was not connected", "Ce poste n'a pas été connecté")}>
            <p>{refused}</p>
            <p>{tr("Nothing was stored on this workstation, and the agent keeps masking.", "Rien n'a été enregistré sur ce poste, et l'agent continue de masquer.")}</p>
          </Notice>
        </div>
      )}
      {s.problem && (
        <div className="panel-note">
          <Notice tone="warn" title={tr("The last report did not go through", "Le dernier rapport n'est pas passé")}>
            <p><code>{s.problem}</code></p>
            <p>
              {tr(
                "The agent keeps masking. Reports are kept on this disk for seven days and sent, oldest first, as soon as the backend answers.",
                "L'agent continue de masquer. Les rapports sont gardés sur ce disque sept jours et envoyés, les plus anciens d'abord, dès que le backend répond.",
              )}
            </p>
          </Notice>
        </div>
      )}

      {s.agent_id ? <ConnectionState s={s} waiting={state === "waiting"} /> : <EnrolForm s={s} refused={refused} setRefused={setRefused} />}
    </section>
  );
}

/** Where a connected workstation reports, as whom, and whether the reports arrive. */
function ConnectionState({ s, waiting }: { s: Supervision; waiting: boolean }) {
  const { busy, working, act } = useStore();
  return (
    <>
      <dl className="facts facts-wide">
        <dt>{tr("Address", "Adresse")}</dt>
        <dd><code>{s.backend}</code></dd>
        <dt>{tr("This workstation", "Ce poste")}</dt>
        <dd><code>{s.agent_id}</code></dd>
        {s.enrolled_at && (
          <>
            <dt>{tr("Connected since", "Connecté depuis")}</dt>
            <dd>{clock(s.enrolled_at)}</dd>
          </>
        )}
        <dt>{tr("Last report", "Dernier rapport")}</dt>
        <dd>
          {s.delivered_at
            ? tr(`${clock(s.delivered_at)}, ${since(s.delivered_at)} ago`, `${clock(s.delivered_at)}, il y a ${since(s.delivered_at)}`)
            : tr("None delivered since the agent started", "Aucun livré depuis le démarrage de l'agent")}
        </dd>
        {s.next_at && (
          <>
            <dt>{s.problem ? tr("Next attempt", "Prochaine tentative") : tr("Next report", "Prochain rapport")}</dt>
            <dd>{until(s.next_at)}</dd>
          </>
        )}
        <dt>{tr("Waiting to be sent", "En attente d'envoi")}</dt>
        <dd>
          {s.queued
            ? tr(
                `${plural(s.queued, "report", "reports")}, the oldest from ${clock(s.oldest_queued)}`,
                `${plural(s.queued, "rapport", "rapports")}, le plus ancien du ${clock(s.oldest_queued)}`,
              )
            : tr("Nothing", "Rien")}
        </dd>
      </dl>
      {waiting && (
        <div className="panel-foot">
          <button
            type="button"
            className="btn"
            disabled={busy || working}
            onClick={() => void act(() => agent.sendNow(), tr("Sending now", "Envoi en cours"), tr("The result shows here within a few seconds.", "Le résultat s'affiche ici dans quelques secondes."))}
          >
            {tr("Send now", "Envoyer maintenant")}
          </button>
        </div>
      )}
    </>
  );
}

/** The form, until a backend has issued this workstation an identity. */
function EnrolForm({ s, refused, setRefused }: { s: Supervision; refused: string; setRefused: (sentence: string) => void }) {
  const { busy, working, act } = useStore();
  // Undefined until typed in, so the field follows what the agent offers.
  const [typed, setTyped] = useState<string>();
  const [token, setToken] = useState("");

  const backend = typed ?? s.offered;
  const sent = enrolmentFrom(backend, token);
  const connect = async () => {
    if (!sent) return;
    setRefused("");
    const done = await act(async () => {
      try {
        await agent.enrol(sent.backend, sent.token);
      } catch (err) {
        setRefused((err as Error).message);
        throw err;
      }
    }, tr("This workstation is connected", "Ce poste est connecté"));
    // Kept after a refusal, for the typo to be corrected in; dropped once spent.
    if (done) setToken("");
  };

  return (
    <form
      className="enrol"
      onSubmit={(e) => {
        e.preventDefault();
        void connect();
      }}
    >
      <label>
        {tr("Address", "Adresse")}
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
        <span className="origin">
          {s.backend
            ? tr(
                <>
                  Set by <code>NEVERSEEN_BACKEND_URL</code> in <code>~/.neverseen/.env</code>: the agent refuses any other.
                </>,
                <>
                  Fixée par <code>NEVERSEEN_BACKEND_URL</code> dans <code>~/.neverseen/.env</code> : l'agent refuse toute autre.
                </>,
              )
            : tr("Neverseen Cloud's own, or the address of a backend your company hosts.", "Celle de Neverseen Cloud, ou l'adresse d'un backend hébergé par votre entreprise.")}
        </span>
      </label>
      <label>
        {tr(
          "Enrolment token, as the dashboard gave it. Single use: it is sent once and never stored.",
          "Jeton d'enrôlement, tel que le tableau de bord l'a donné. À usage unique : il est envoyé une fois et jamais enregistré.",
        )}
        <input
          className="input"
          type="password"
          value={token}
          disabled={busy}
          aria-invalid={refused ? true : undefined}
          onChange={(e) => setToken(e.target.value)}
          autoComplete="off"
          spellCheck={false}
          placeholder="nse_…"
        />
      </label>
      <div className="btn-row">
        <button type="submit" className="btn btn-primary" disabled={busy || working || !sent}>
          {working ? tr("Connecting…", "Connexion…") : tr("Connect this workstation", "Connecter ce poste")}
        </button>
      </div>
    </form>
  );
}

/** How long until a date the agent gave, in the words of `since`. */
function until(iso: string): string {
  const m = Math.ceil((new Date(iso).getTime() - Date.now()) / 60000);
  if (m <= 0) return tr("Any moment now", "D'un instant à l'autre");
  return tr(`In about ${m} min`, `Dans environ ${m} min`);
}

function Carries() {
  const { busy } = useStore();
  // Undefined while closed; the document, or the sentence saying why there is none.
  const [report, setReport] = useState<string>();
  const show = async () => {
    try {
      setReport(JSON.stringify(await agent.nextReport(), null, 2));
    } catch (err) {
      setReport((err as Error).message);
    }
  };

  const item = (text: ReactNode, detail?: ReactNode) => (
    <li>
      <span>
        {text}
        {detail && <small>{detail}</small>}
      </span>
    </li>
  );

  return (
    <section className="panel" aria-labelledby="carries-title">
      <header className="panel-head">
        <h2 id="carries-title">{tr("What a report carries", "Ce que contient un rapport")}</h2>
        <span className="muted">{tr("Every five minutes", "Toutes les cinq minutes")}</span>
      </header>
      <div className="carries">
        <div className="carries-sent">
          <h3><Check size={13} aria-hidden /> {tr("Sent", "Envoyé")}</h3>
          <ul>
            {item(tr("How many requests the agent relayed", "Le nombre de requêtes relayées par l'agent"))}
            {item(
              tr("How many values it masked, by category", "Le nombre de valeurs masquées, par catégorie"),
              tr("“12 email addresses”, never one of them", "« 12 adresses e-mail », jamais l'une d'elles"),
            )}
            {item(tr("How many tokens went to each model", "Le nombre de tokens par modèle"))}
            {item(
              tr("How many sessions and tool calls", "Le nombre de sessions et d'appels d'outils"),
              tr("Which tools and which well-known programs, from a fixed list of names", "Quels outils et quels programmes courants, d'après une liste fermée de noms"),
            )}
            {item(
              tr("How the agent is configured", "La configuration de l'agent"),
              tr(
                <>
                  What is switched off, the substitution mode, whether <code>-a</code>, <code>-v</code> or <code>-l</code> is on
                </>,
                <>
                  Ce qui est désactivé, le mode de substitution, si <code>-a</code>, <code>-v</code> ou <code>-l</code> est actif
                </>,
              ),
            )}
            {item(
              tr("This machine's name and local addresses", "Le nom et les adresses locales de cette machine"),
              tr(
                "The two fields that are personal data: they are how a dashboard tells two workstations apart",
                "Les deux champs qui sont des données personnelles : c'est ce qui distingue deux postes dans un tableau de bord",
              ),
            )}
          </ul>
        </div>
        <div className="carries-never">
          <h3><X size={13} aria-hidden /> {tr("Never sent", "Jamais envoyé")}</h3>
          <ul>
            {item(tr("A prompt or an answer", "Un prompt ou une réponse"))}
            {item(tr("A masked value, or what replaced it", "Une valeur masquée, ou ce qui l'a remplacée"))}
            {item(tr("A file name, a command line or a tool call's arguments", "Un nom de fichier, une ligne de commande ou les arguments d'un appel d'outil"))}
            {item(
              tr("Your provider's API key", "La clé d'API de votre fournisseur"),
              tr("The agent holds none: it forwards yours untouched", "L'agent n'en détient aucune : il transmet la vôtre telle quelle"),
            )}
          </ul>
        </div>
      </div>
      <div className="panel-foot">
        <button type="button" className="btn" disabled={busy} onClick={() => void show()}>
          {tr("See what the next report holds", "Voir ce que contient le prochain rapport")}
        </button>
        <span className="muted small">{tr("The exact document, as it stands now.", "Le document exact, tel qu'il est maintenant.")}</span>
      </div>

      <Dialog open={report !== undefined} onClose={() => setReport(undefined)} title={tr("The next report", "Le prochain rapport")} wide>
        <p>
          {tr(
            "What this agent has counted since its last report, in the shape it is sent. Not connected, nothing is sent, and what was counted before a connection is dropped.",
            "Ce que cet agent a compté depuis son dernier rapport, sous la forme envoyée. Sans connexion rien n'est envoyé, et ce qui a été compté avant une connexion est abandonné.",
          )}
        </p>
        <pre className="json">{report}</pre>
      </Dialog>
    </section>
  );
}
