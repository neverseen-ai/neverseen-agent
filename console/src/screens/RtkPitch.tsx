import { useState, type ReactNode } from "react";
import { ArrowRight, Check, Gauge, TrendingDown, Zap } from "lucide-react";
import { tr } from "../i18n";
import { CommandLine } from "../ui/controls";

// The rtk screen on a workstation without rtk: what it is for, and how to install it.
// Drawn only where the agent said it found none (decisions.rtkScreen) — and again, with
// the steps behind it ticked, once rtk is there and has recorded nothing yet.
//
// rtk is a third party's. Every command here is rtk's own README's, and the licence is
// said as that README says it: the CLI is Apache-2.0 and its engine is not.
//
// TODO: the commands are spelled out here, so an rtk that renames one leaves this page
// offering a line that fails. They are prose about another project rather than a set
// this agent owns, which is why they are not served; the upgrade, the day one drifts,
// is a link to rtk's installation guide in place of the lines.

const RTK_SOURCE = "https://github.com/rtk-ai/rtk";

const INSTALLERS = [
  { name: "Homebrew", command: "brew install rtk" },
  { name: "Script", command: "curl -fsSL https://raw.githubusercontent.com/rtk-ai/rtk/refs/heads/master/install.sh | sh" },
  { name: "Windows", command: "winget install rtk-ai.rtk" },
];

/** What `go test ./...` prints over nine packages, shortened to fit: the example's
 * left-hand side. The right-hand side is the one line rtk makes of it. */
const RAW = ["cmd/neverseen", "internal/detector", "internal/proxy", "internal/service", "internal/telemetry", "internal/tray", "internal/vault", "pkg/pii", "pkg/telemetry"];

export function RtkPitch({ path }: { path?: string }) {
  // With a path, rtk is installed and answering: the steps are behind, bar the hook,
  // which this agent cannot see.
  const fresh = !!path;
  const [installer, setInstaller] = useState(0);

  return (
    <div className="page">
      <header className="pitch-hero">
        <span className={`pitch-eyebrow ${fresh ? "pitch-found" : ""}`}>
          <i aria-hidden />
          {fresh ? tr(`rtk · found at ${path}`, `rtk · trouvé dans ${path}`) : tr("rtk · not on this workstation", "rtk · absent de ce poste")}
        </span>
        <h1>
          {tr("Your coding agent reads every line a command prints.", "Votre agent de code lit chaque ligne qu'affiche une commande.")}{" "}
          <em>{tr("Most of them say nothing.", "La plupart ne disent rien.")}</em>
        </h1>
        <p className="lede">
          {tr(
            <>
              rtk is a small command-line tool that shortens the output of the commands an agent runs — tests, <code>git</code>, builds, searches — before a
              model reads it. Fewer tokens per command, and a conversation that lasts longer before it has to be summarised.
            </>,
            <>
              rtk est un petit outil en ligne de commande qui raccourcit la sortie des commandes lancées par un agent — tests, <code>git</code>, builds,
              recherches — avant qu'un modèle ne la lise. Moins de tokens par commande, et une conversation qui dure plus longtemps avant de devoir être
              résumée.
            </>,
          )}
        </p>
        <div className="btn-row">
          <button type="button" className="btn btn-primary" onClick={() => document.getElementById("rtk-install")?.scrollIntoView({ behavior: "smooth" })}>
            {tr("Install rtk", "Installer rtk")}
          </button>
          <a className="btn" href={RTK_SOURCE} target="_blank" rel="noreferrer">
            {tr("Read the source on GitHub", "Lire le code sur GitHub")}
          </a>
        </div>
      </header>

      <div className="pitch-compare">
        <Terminal side={tr("without rtk", "sans rtk")} lines={RAW.length} share={100}>
          {RAW.map((pkg) => (
            <span key={pkg} className="pitch-dim">{`ok   …/${pkg}\n`}</span>
          ))}
        </Terminal>
        <ArrowRight className="pitch-arrow" size={20} aria-hidden />
        <Terminal side={tr("with rtk", "avec rtk")} lines={1} share={100 / RAW.length} after>
          <span className="pitch-ok">{`Go test: 497 passed in ${RAW.length} packages`}</span>
        </Terminal>
      </div>
      <p className="fine">
        {tr(
          "An example. A test that fails is kept: rtk cuts the lines that pass, not the ones that went wrong.",
          "Un exemple. Un test en échec est conservé : rtk retire les lignes qui passent, pas celles qui ont échoué.",
        )}
      </p>

      <section className="block" aria-labelledby="rtk-why-title">
        <h2 id="rtk-why-title">{tr("Why it is worth five minutes", "Pourquoi cela vaut cinq minutes")}</h2>
        <p className="lede">{tr("Three things change, and none of them is how you work.", "Trois choses changent, et aucune n'est votre façon de travailler.")}</p>
        <div className="pitch-reasons">
          <Reason icon={<TrendingDown size={16} aria-hidden />} title={tr("Fewer tokens, every turn", "Moins de tokens, à chaque tour")}>
            {tr(
              "A command's output stays in the conversation and is sent again with every turn that follows. What rtk cuts once is saved on each of them.",
              "La sortie d'une commande reste dans la conversation et repart à chaque tour suivant. Ce que rtk retire une fois est économisé à chacun d'eux.",
            )}
          </Reason>
          <Reason icon={<Zap size={16} aria-hidden />} title={tr("Nothing to remember", "Rien à retenir")}>
            {tr(
              <>
                Once set up, a hook rewrites the agent's commands: it asks for <code>git status</code> and gets rtk's version of it. You type nothing differently.
              </>,
              <>
                Une fois en place, un hook réécrit les commandes de l'agent : il demande <code>git status</code> et reçoit la version de rtk. Vous ne tapez rien
                autrement.
              </>,
            )}
          </Reason>
          <Reason icon={<Gauge size={16} aria-hidden />} title={tr("Measured, on your machine", "Mesuré, sur votre machine")}>
            {tr(
              "rtk keeps its own count of what it cut, locally. This screen draws it day by day, so the saving is a figure you can check rather than a promise.",
              "rtk tient son propre compte de ce qu'il a retiré, en local. Cet écran le trace jour par jour : l'économie est un chiffre vérifiable, pas une promesse.",
            )}
          </Reason>
        </div>
      </section>

      <section className="block" aria-labelledby="rtk-fit-title">
        <h2 id="rtk-fit-title">{tr("Where it sits beside Neverseen", "Sa place à côté de Neverseen")}</h2>
        <p className="lede">
          {tr(
            "Two tools on the same road, each doing one thing. Neverseen works the same with or without rtk.",
            "Deux outils sur le même trajet, chacun avec un seul rôle. Neverseen fonctionne de la même façon avec ou sans rtk.",
          )}
        </p>
        <ol className="pitch-flow">
          <li>
            <strong>{tr("A command runs", "Une commande s'exécute")}</strong>
            <span>{tr("Your agent calls a tool and it prints its output.", "Votre agent appelle un outil, qui affiche sa sortie.")}</span>
          </li>
          <li className="pitch-flow-rtk">
            <small>rtk</small>
            <strong>{tr("Shortens it", "La raccourcit")}</strong>
            <span>{tr("What repeats or says nothing is cut.", "Ce qui se répète ou ne dit rien est retiré.")}</span>
          </li>
          <li className="pitch-flow-us">
            <small>Neverseen</small>
            <strong>{tr("Masks it", "La masque")}</strong>
            <span>{tr("Personal data and credentials are replaced.", "Les données personnelles et les identifiants sont remplacés.")}</span>
          </li>
          <li>
            <strong>{tr("The model reads it", "Le modèle la lit")}</strong>
            <span>{tr("Shorter, and with nothing it should not see.", "Plus courte, et sans rien qu'il ne devrait voir.")}</span>
          </li>
        </ol>
      </section>

      <section className="block" id="rtk-install" aria-labelledby="rtk-install-title">
        <h2 id="rtk-install-title">{tr("Set it up", "Le mettre en place")}</h2>
        <p className="lede">
          {tr(
            "Three commands in a terminal. rtk is a third party's tool: Neverseen neither ships it nor installs it for you.",
            "Trois commandes dans un terminal. rtk est l'outil d'un tiers : Neverseen ne le fournit pas et ne l'installe pas pour vous.",
          )}
        </p>

        <ol className="pitch-steps">
          <Step n={1} done={fresh} title={tr("Install the binary", "Installer le binaire")}>
            <div className="segmented" role="radiogroup" aria-label={tr("Installer", "Installateur")}>
              {INSTALLERS.map((i, at) => (
                <button key={i.name} type="button" role="radio" aria-checked={at === installer} onClick={() => setInstaller(at)}>
                  {i.name}
                </button>
              ))}
            </div>
            <CommandLine text={INSTALLERS[installer].command} />
          </Step>
          <Step n={2} title={tr("Hook it into your coding agent", "Le brancher sur votre agent de code")}>
            <CommandLine text="rtk init -g" />
            <p>
              {tr(
                <>
                  For Claude Code; then restart it. Another agent is named with <code>--agent</code> — <code>cursor</code>, <code>windsurf</code>,{" "}
                  <code>cline</code> and others, listed by <code>rtk init --help</code>.
                </>,
                <>
                  Pour Claude Code ; redémarrez-le ensuite. Un autre agent se nomme avec <code>--agent</code> — <code>cursor</code>, <code>windsurf</code>,{" "}
                  <code>cline</code> et d'autres, listés par <code>rtk init --help</code>.
                </>,
              )}
            </p>
          </Step>
          <Step n={3} done={fresh} title={tr("Check it answers", "Vérifier qu'il répond")}>
            <CommandLine text="rtk gain" />
            <p>
              {tr(
                "It must show the savings dashboard. “Command not found”, or anything else, means another tool of the same name is installed: one exists on crates.io.",
                "Il doit afficher le tableau des économies. « Command not found », ou toute autre chose, signifie qu'un autre outil du même nom est installé : il en existe un sur crates.io.",
              )}
            </p>
          </Step>
        </ol>

        <p className={`pitch-waiting ${fresh ? "pitch-found" : ""}`} role="status">
          <i aria-hidden />
          <span>
            {fresh
              ? tr(
                  <>
                    <strong>rtk is installed and has recorded nothing yet.</strong> Run a command through your coding agent: the first day's column appears here
                    within half a minute of rtk shortening one.
                  </>,
                  <>
                    <strong>rtk est installé et n'a encore rien enregistré.</strong> Lancez une commande depuis votre agent de code : la colonne du premier jour
                    apparaît ici dans la demi-minute qui suit.
                  </>,
                )
              : tr(
                  <>
                    <strong>This page is watching for rtk.</strong> No restart and no reload: it changes within a few seconds of the install.
                  </>,
                  <>
                    <strong>Cette page guette rtk.</strong> Ni redémarrage ni rechargement : elle change quelques secondes après l'installation.
                  </>,
                )}
          </span>
        </p>
      </section>

      <section className="block" aria-labelledby="rtk-know-title">
        <h2 id="rtk-know-title">{tr("Worth knowing before you install", "À savoir avant d'installer")}</h2>
        <div className="panel pitch-know">
          <dl className="facts facts-wide">
            <dt>{tr("Where the agent looks", "Où l'agent cherche")}</dt>
            <dd>
              {tr(
                <>
                  Your <code>PATH</code>, then Homebrew, <code>~/.cargo/bin</code> and <code>~/.local/bin</code>
                </>,
                <>
                  Votre <code>PATH</code>, puis Homebrew, <code>~/.cargo/bin</code> et <code>~/.local/bin</code>
                </>,
              )}
              <span className="origin">{tr("Installed anywhere else, rtk works and this screen does not see it.", "Installé ailleurs, rtk fonctionne et cet écran ne le voit pas.")}</span>
            </dd>
            <dt>{tr("What rtk shortens", "Ce que rtk raccourcit")}</dt>
            <dd>
              {tr("The commands your agent runs in a shell", "Les commandes que votre agent lance dans un shell")}
              <span className="origin">
                {tr(
                  "The hook fires on those alone: an agent's built-in tools for reading and searching files do not pass through it.",
                  "Le hook ne se déclenche que sur celles-là : les outils intégrés d'un agent pour lire et chercher dans les fichiers n'y passent pas.",
                )}
              </span>
            </dd>
            <dt>{tr("What this screen reads", "Ce que lit cet écran")}</dt>
            <dd>
              {tr("rtk's daily totals, and nothing else", "Les totaux journaliers de rtk, et rien d'autre")}
              <span className="origin">{tr("Never its history, which holds the command lines themselves.", "Jamais son historique, qui contient les lignes de commande.")}</span>
            </dd>
            <dt>{tr("Its licence", "Sa licence")}</dt>
            <dd>
              {tr("The command-line tool is Apache-2.0", "L'outil en ligne de commande est sous Apache-2.0")}
              <span className="origin">
                {tr(
                  "Its engine is source-available under BUSL-1.1, free for production use. rtk also publishes an edition that is Apache-2.0 alone.",
                  "Son moteur est à code source disponible sous BUSL-1.1, gratuit en production. rtk publie aussi une édition uniquement sous Apache-2.0.",
                )}
              </span>
            </dd>
            <dt>{tr("If you change your mind", "Si vous changez d'avis")}</dt>
            <dd>
              {tr("Uninstall rtk and this page comes back", "Désinstallez rtk et cette page revient")}
              <span className="origin">{tr("Masking never depended on it: rtk is not on the path a request takes.", "Le masquage n'en a jamais dépendu : rtk n'est pas sur le chemin d'une requête.")}</span>
            </dd>
          </dl>
        </div>
      </section>
    </div>
  );
}

/** One side of the example: the command, what it printed, and how much of it there is. */
function Terminal({ side, lines, share, after, children }: { side: string; lines: number; share: number; after?: boolean; children: ReactNode }) {
  return (
    <div className={`pitch-term ${after ? "pitch-term-after" : ""}`}>
      <div className="pitch-term-head">
        <b>go test ./...</b>
        <span>{side}</span>
      </div>
      <pre>{children}</pre>
      <div className="pitch-term-foot">
        <div className="pitch-meter">
          <span style={{ width: `${share}%` }} />
        </div>
        <div className="pitch-term-count">
          <span>{tr("read by the model", "lu par le modèle")}</span>
          <b>{tr(`${lines} ${lines === 1 ? "line" : "lines"}`, `${lines} ${lines === 1 ? "ligne" : "lignes"}`)}</b>
        </div>
      </div>
    </div>
  );
}

function Reason({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return (
    <div className="pitch-reason">
      <span className="pitch-reason-icon">{icon}</span>
      <h3>{title}</h3>
      <p>{children}</p>
    </div>
  );
}

function Step({ n, done, title, children }: { n: number; done?: boolean; title: string; children: ReactNode }) {
  return (
    <li className={done ? "pitch-done" : undefined}>
      <span className="pitch-step-n">{done ? <Check size={14} aria-label={tr("Done", "Fait")} /> : n}</span>
      <div className="pitch-step-body">
        <h3>{title}</h3>
        {children}
      </div>
    </li>
  );
}
