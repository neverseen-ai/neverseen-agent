import { useCallback, useEffect, useState } from "react";
import { FlaskConical, Gauge, Keyboard, Lock, Monitor, Moon, Plug, Search, Server, ShieldCheck, Sun } from "lucide-react";
import { MARK_FOR, SCREENS, dropParam, parseRoute, useStore, type Screen } from "./store";
import { Palette, SCREEN_LABELS } from "./Palette";
import { inClear } from "../agent/decisions";
import { LANGS, setLang, tr, useLang } from "../i18n";
import { Kbd, MOD } from "../ui/controls";
import { Mark } from "../ui/Mark";
import { Dialog } from "../ui/overlays";
import { lockedSentence } from "../agent/client";
import { Overview } from "../screens/Overview";
import { Masked } from "../screens/Masked";
import { TryIt } from "../screens/TryIt";
import { Connect } from "../screens/Connect";
import { Service } from "../screens/Service";

const NAV: { screen: Screen; icon: typeof Gauge }[] = [
  { screen: "overview", icon: Gauge },
  { screen: "masked", icon: ShieldCheck },
  { screen: "try", icon: FlaskConical },
  { screen: "connect", icon: Plug },
  { screen: "service", icon: Server },
];

type Theme = "system" | "light" | "dark";

function readTheme(): Theme {
  const fromUrl = parseRoute(location.hash).params.get("theme");
  if (fromUrl === "light" || fromUrl === "dark") return fromUrl;
  try {
    const t = localStorage.getItem("neverseen.theme");
    if (t === "light" || t === "dark") return t;
  } catch {
    // Storage can be refused (a private window, some file:// origins): follow the system.
  }
  return "system";
}

export function App() {
  const { snap, verdict, route, go, working } = useStore();
  // Read here so that a change of language redraws everything under the App.
  const language = useLang();
  const [theme, setTheme] = useState<Theme>(readTheme);
  const [palette, setPalette] = useState(false);
  const [help, setHelp] = useState(false);

  useEffect(() => {
    const root = document.documentElement;
    if (theme === "system") delete root.dataset.theme;
    else root.dataset.theme = theme;
    try {
      if (theme === "system") localStorage.removeItem("neverseen.theme");
      else localStorage.setItem("neverseen.theme", theme);
    } catch {
      // Not remembered, still applied.
    }
  }, [theme]);

  const toggleTheme = useCallback(() => {
    dropParam("theme");
    const dark = theme === "dark" || (theme === "system" && matchMedia("(prefers-color-scheme: dark)").matches);
    setTheme(dark ? "light" : "dark");
  }, [theme]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const typing = e.target instanceof HTMLElement && /^(INPUT|TEXTAREA|SELECT)$/.test(e.target.tagName);
      const mod = e.metaKey || e.ctrlKey;
      if (mod && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((p) => !p);
      } else if (mod && /^[1-9]$/.test(e.key) && Number(e.key) <= SCREENS.length) {
        e.preventDefault();
        go(SCREENS[Number(e.key) - 1]);
      } else if (!typing && e.key === "/") {
        e.preventDefault();
        if (route.screen !== "masked") go("masked");
        setTimeout(() => document.getElementById("search")?.focus(), 0);
      } else if (!typing && e.key === "?") {
        setHelp(true);
      }
    };
    addEventListener("keydown", onKey);
    return () => removeEventListener("keydown", onKey);
  }, [go, route.screen]);

  const off = snap ? inClear(snap.health) : [];
  const status = {
    full: tr("Masking", "Masquage actif"),
    partial: tr(`Masking, ${off.length} in clear`, `Masquage, ${off.length} en clair`),
    none: tr("Not masking", "Aucun masquage"),
    stopped: tr("Not running", "Arrêté"),
  }[verdict];

  const Screen = { overview: Overview, masked: Masked, try: TryIt, connect: Connect, service: Service }[route.screen];

  return (
    <div className={`app ${working ? "is-busy" : ""}`}>
      <nav className="sidebar" aria-label="Console">
        <div className="brand">
          <Mark state="masking" width={22} />
          <span>Neverseen</span>
        </div>

        <button type="button" className={`status status-${verdict}`} onClick={() => go("overview")}>
          <Mark state={MARK_FOR[verdict]} width={18} label={status} />
          <span className="status-text">
            <strong>{status}</strong>
            <span>{snap ? snap.addr : "…"}</span>
          </span>
        </button>

        <button type="button" className="find" onClick={() => setPalette(true)}>
          <Search size={14} aria-hidden /> <span>{tr("Go to", "Aller à")}</span> <Kbd>{MOD} K</Kbd>
        </button>

        <ul className="nav">
          {NAV.map(({ screen, icon: Icon }, i) => (
            <li key={screen}>
              <a
                href={`#/${screen}`}
                aria-current={route.screen === screen ? "page" : undefined}
                onClick={(e) => {
                  e.preventDefault();
                  go(screen);
                }}
              >
                <Icon size={16} aria-hidden />
                <span>{SCREEN_LABELS[screen]}</span>
                {screen === "masked" && verdict === "partial" && <span className="nav-badge nav-badge-warn">{off.length}</span>}
                <span className="nav-key" aria-hidden>
                  {MOD === "⌘" ? "⌘" : "^"}
                  {i + 1}
                </span>
              </a>
            </li>
          ))}
        </ul>

        <div className="sidebar-foot">
          <div className="segmented segmented-small" role="radiogroup" aria-label={tr("Theme", "Thème")}>
            {(
              [
                ["system", Monitor, tr("Follow the system", "Suivre le système")],
                ["light", Sun, tr("Light", "Clair")],
                ["dark", Moon, tr("Dark", "Sombre")],
              ] as const
            ).map(([t, Icon, label]) => (
              <button key={t} type="button" role="radio" aria-checked={theme === t} aria-label={label} title={label} onClick={() => { dropParam("theme"); setTheme(t); }}>
                <Icon size={14} aria-hidden />
              </button>
            ))}
          </div>
          <div className="segmented segmented-small" role="radiogroup" aria-label={tr("Language", "Langue")}>
            {LANGS.map((l) => (
              <button key={l.code} type="button" role="radio" aria-checked={language === l.code} aria-label={l.label} title={l.label} lang={l.code} onClick={() => { dropParam("lang"); setLang(l.code); }}>
                {l.code.toUpperCase()}
              </button>
            ))}
          </div>
          <button type="button" className="btn btn-quiet btn-icon" aria-label={tr("Keyboard shortcuts", "Raccourcis clavier")} title={tr("Keyboard shortcuts (?)", "Raccourcis clavier (?)")} onClick={() => setHelp(true)}>
            <Keyboard size={15} aria-hidden />
          </button>
        </div>
      </nav>

      <main className="main" id="main">
        {snap?.locked && (
          // Above every screen, so no control is tried before the page has said why it
          // will not work.
          <p className="locked-banner" role="status">
            <Lock size={14} aria-hidden /> {lockedSentence()}
          </p>
        )}
        {snap ? <Screen /> : <div className="page loading" aria-busy="true" />}
      </main>

      <Palette open={palette} onClose={() => setPalette(false)} toggleTheme={toggleTheme} />

      <Dialog open={help} onClose={() => setHelp(false)} title={tr("Keyboard shortcuts", "Raccourcis clavier")}>
        <dl className="shortcuts">
          <dt><Kbd>{MOD} K</Kbd></dt>
          <dd>{tr("Go to a screen, a category or a country", "Aller à un écran, une catégorie ou un pays")}</dd>
          <dt><Kbd>{MOD} 1</Kbd> {tr("to", "à")} <Kbd>{MOD} {SCREENS.length}</Kbd></dt>
          <dd>{tr("Switch screen", "Changer d'écran")}</dd>
          <dt><Kbd>/</Kbd></dt>
          <dd>{tr("Find a category, a country or a prefix", "Chercher une catégorie, un pays ou un préfixe")}</dd>
          <dt><Kbd>?</Kbd></dt>
          <dd>{tr("These shortcuts", "Ces raccourcis")}</dd>
        </dl>
      </Dialog>
    </div>
  );
}
