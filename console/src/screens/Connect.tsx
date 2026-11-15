import { useState } from "react";
import { Globe } from "lucide-react";
import { useStore } from "../app/store";
import type { HealthTool, Snapshot } from "../agent/types";
import { ago } from "../copy";
import { served, tr } from "../i18n";
import { CommandLine, Notice } from "../ui/controls";

type Shell = "posix" | "powershell";

// internal/proxy/shellenv.go PointAtFor, spelled for each shell from the table the agent
// serves (Health.tools, from shellTools): only verified pairs, and the caveat travels
// with every line it belongs to. A provider with no entry has no agreed variable, so
// it is handed over as the bare URL rather than as a guess.
function pointAt(tool: HealthTool | undefined, code: string, addr: string, shell: Shell): string {
  const url = `http://${addr}/${code}`;
  if (!tool) return url;
  if (tool.cli && shell === "powershell") return `$env:${tool.variable} = "${url}"; ${tool.cli}`;
  if (tool.cli) return `${tool.variable}=${url} ${tool.cli}`;
  return shell === "powershell" ? `$env:${tool.variable} = "${url}"` : `export ${tool.variable}=${url}`;
}

export function Connect() {
  const { snap, verdict } = useStore();
  const [shell, setShell] = useState<Shell>(/Win/.test(navigator.platform) ? "powershell" : "posix");
  if (!snap) return null;
  const addr = snap.addr.startsWith("0.0.0.0") ? `127.0.0.1:${snap.addr.split(":")[1]}` : snap.addr;
  const providers = snap.health.providers;
  const tools = new Map((snap.health.tools ?? []).map((t) => [t.provider, t]));

  return (
    <div className="page">
      <header className="page-head">
        <div>
          <h1>{tr("Connect a tool", "Connecter un outil")}</h1>
          <p className="lede">
            {tr(
              "For the agent to mask, a tool has to send its requests here rather than straight to the provider: only its base address changes. The agent needs no key of its own; your tool's key passes through untouched.",
              "Pour que l'agent masque, un outil doit lui envoyer ses requêtes au lieu de les envoyer directement au fournisseur : seule son adresse de base change. L'agent n'a besoin d'aucune clé ; celle de votre outil passe telle quelle.",
            )}
          </p>
        </div>
      </header>

      {verdict === "stopped" && (
        <Notice tone="warn" title={tr("The agent is not running, so these lines lead nowhere for now", "L'agent est arrêté : ces lignes ne mènent nulle part pour l'instant")}>
          {tr(
            "A tool pointed here fails until the agent starts. The shell profile line below does nothing while it is stopped, which is why it is the one to keep.",
            "Un outil pointé ici échoue tant que l'agent n'a pas démarré. La ligne de profil ci-dessous ne fait rien pendant l'arrêt : c'est pourquoi c'est elle qu'il faut garder.",
          )}
        </Notice>
      )}

      <section className="block" aria-labelledby="every-title">
        {/* The shell switch sits on the first line it rewrites, not across the page from it. */}
        <div className="block-head">
          <h2 id="every-title">{tr("Every new shell", "Chaque nouveau terminal")}</h2>
          <div className="segmented" role="radiogroup" aria-label="Shell">
            <button type="button" role="radio" aria-checked={shell === "posix"} onClick={() => setShell("posix")}>
              zsh, bash
            </button>
            <button type="button" role="radio" aria-checked={shell === "powershell"} onClick={() => setShell("powershell")}>
              PowerShell
            </button>
          </div>
        </div>
        <p className="lede">
          {tr(
            "This line, in your shell profile, connects every new terminal to the agent, but only while it is running. If it is stopped, your tools go straight to their provider: unmasked, but working.",
            "Cette ligne, dans votre profil de shell, branche chaque nouveau terminal sur l'agent, mais seulement s'il est en marche. S'il est arrêté, vos outils vont directement chez leur fournisseur : sans masquage, mais sans panne.",
          )}
        </p>
        {/* --shell named: without it `neverseen env` writes for the platform's own shell, so
            PowerShell on a Mac was handed `export` lines and Git Bash on Windows `$env:` ones. */}
        <CommandLine text={shell === "powershell" ? "neverseen env --shell powershell | Invoke-Expression" : 'eval "$(neverseen env --shell posix)"'} />
        <p className="fine">
          {tr("./install.sh --shell adds it to your shell profile for you.", "./install.sh --shell l'ajoute pour vous à votre profil de shell.")}
        </p>
      </section>

      <section className="block" aria-labelledby="one-title">
        <h2 id="one-title">{tr("One tool, one run", "Un outil, une exécution")}</h2>
        <p className="lede">
          {tr(
            "To run one tool through the agent, once. Keep these lines out of your shell profile: unlike the one above, they point at the agent even when it is stopped, and the tool then fails.",
            "Pour lancer un outil une fois à travers l'agent. Ne mettez pas ces lignes dans votre profil de shell : contrairement à celle du dessus, elles visent l'agent même quand il est arrêté, et l'outil échoue alors.",
          )}
        </p>
        <div className="providers">
          {providers.filter((code) => tools.has(code)).map((code) => {
            const tool = tools.get(code)!;
            return (
              <div key={code} className="provider">
                <div className="provider-head">
                  <span className="provider-name">{code}</span>
                  {tool.cli && <span className="tag">{tool.cli}</span>}
                </div>
                <CommandLine text={pointAt(tool, code, addr, shell)} />
                {tool.caveat && <p className="caveat">{served(tool.caveat)}</p>}
              </div>
            );
          })}
        </div>

        <h3 className="sub">{tr("Other providers", "Autres fournisseurs")}</h3>
        <p className="fine">
          {tr(
            "These have no standard variable: paste the address into your tool's base URL setting.",
            "Ceux-ci n'ont pas de variable standard : collez l'adresse dans le réglage « URL de base » de votre outil.",
          )}
        </p>
        <ul className="other-providers">
          {providers.filter((code) => !tools.has(code)).map((code) => (
            // No name beside it: the URL ends in the provider's name, and the room goes to showing it whole.
            <li key={code}>
              <CommandLine text={pointAt(undefined, code, addr, shell)} />
            </li>
          ))}
        </ul>
      </section>

      <Extension snap={snap} />
    </div>
  );
}

// What the extension is, when it last called this agent, and how to give it the key.
// The last call is all the agent knows: a key refused is refused before anything is
// remembered, and an extension that never called says nothing about being installed.
function Extension({ snap }: { snap: Snapshot }) {
  const agent = snap.today.agent;
  const seen = agent?.extension_seen_at;
  return (
    <section className="block" aria-labelledby="ext-title">
      <h2 id="ext-title">{tr("Browser extension", "Extension de navigateur")}</h2>
      <div className="panel ext">
        <Globe size={18} aria-hidden className="ext-icon" />
        <div>
          {agent && (
            <p className={seen ? "ext-state text-ok" : "ext-state muted"}>
              {seen
                ? tr(`Last call ${ago(seen)}`, `Dernier appel ${ago(seen)}`)
                : tr("No call since the agent started", "Aucun appel depuis le démarrage de l'agent")}
            </p>
          )}
          <p className="fine">
            {tr(
              "Masks what you type on claude.ai. A message it cannot mask is not sent. An answer it cannot put back in clear is still shown, with its placeholders.",
              "Masque ce que vous tapez sur claude.ai. Un message qu'elle ne peut pas masquer n'est pas envoyé. Une réponse qu'elle ne peut pas remettre en clair s'affiche quand même, avec ses étiquettes.",
            )}
          </p>
        </div>
        <div className="ext-key">
          <p className="small muted">{tr("Paste the key into the extension's options:", "Collez la clé dans les options de l'extension :")}</p>
          <CommandLine text="neverseen key" />
        </div>
      </div>
    </section>
  );
}
