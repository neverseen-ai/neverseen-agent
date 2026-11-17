import { useEffect, useMemo, useRef, useState } from "react";
import { Search } from "lucide-react";
import { useStore, type Screen } from "./store";
import { countryName } from "../copy";
import { lang, tr } from "../i18n";

interface Item {
  id: string;
  kind: "screen" | "category" | "country" | "action";
  label: string;
  hint?: string;
  run: () => void;
}

export const SCREEN_LABELS: Record<Screen, string> = {
  get overview() {
    return tr("Overview", "Vue d'ensemble");
  },
  get masked() {
    return tr("What is masked", "Ce qui est masqué");
  },
  get try() {
    return tr("Try it", "Essayer");
  },
  get connect() {
    return tr("Connect an LLM agent", "Connecter un agent LLM");
  },
  get rtk() {
    return tr("rtk savings", "Économies rtk");
  },
  get cloud() {
    return "Neverseen Cloud";
  },
  get service() {
    return tr("Service", "Service");
  },
};

const KIND_LABELS: Record<Item["kind"], string> = {
  get screen() {
    return tr("Screen", "Écran");
  },
  get category() {
    return tr("Category", "Catégorie");
  },
  get country() {
    return tr("Country", "Pays");
  },
  get action() {
    return tr("Action", "Action");
  },
};

export function Palette({ open, onClose, toggleTheme }: { open: boolean; onClose: () => void; toggleTheme: () => void }) {
  const { snap, go, change, verdict } = useStore();
  const [q, setQ] = useState("");
  const [at, setAt] = useState(0);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (open) {
      setQ("");
      setAt(0);
      setTimeout(() => input.current?.focus(), 0);
    }
  }, [open]);

  const items = useMemo<Item[]>(() => {
    const out: Item[] = (Object.keys(SCREEN_LABELS) as Screen[]).map((s) => ({
      id: `s-${s}`,
      kind: "screen",
      label: SCREEN_LABELS[s],
      run: () => go(s),
    }));
    if (verdict === "partial") out.push({ id: "a-all", kind: "action", label: tr("Mask everything again", "Tout masquer à nouveau"), run: () => void change({ off: [] }, tr("Everything is masked again", "Tout est à nouveau masqué"), { undo: true }) });
    out.push({ id: "a-theme", kind: "action", label: tr("Switch between light and dark", "Basculer entre clair et sombre"), run: toggleTheme });
    for (const g of snap?.health.groups ?? []) {
      for (const c of g.categories) {
        out.push({
          id: `c-${c.code}`,
          kind: "category",
          label: c.label,
          hint: [g.label, ...(c.notations ?? [])].join(", "),
          run: () => go("masked", { tab: g.credentials ? "credentials" : undefined, q: c.label }),
        });
      }
    }
    for (const l of snap?.health.available_locales ?? []) {
      out.push({ id: `l-${l}`, kind: "country", label: countryName(l), hint: l, run: () => go("masked", { q: countryName(l) }) });
    }
    return out;
    // The language is a dependency of its own: every label above is read through it.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [snap, verdict, go, change, toggleTheme, lang()]);

  const needle = q.trim().toLowerCase();
  const shown = (needle ? items.filter((i) => `${i.label} ${i.hint ?? ""}`.toLowerCase().includes(needle)) : items.filter((i) => i.kind !== "category" && i.kind !== "country")).slice(0, 12);

  const pick = (i: Item | undefined) => {
    if (!i) return;
    onClose();
    i.run();
  };

  if (!open) return null;
  return (
    <div className="palette-backdrop" onMouseDown={(e) => e.target === e.currentTarget && onClose()}>
      <div className="palette" role="dialog" aria-modal="true" aria-label={tr("Go to", "Aller à")}>
        <div className="palette-input">
          <Search size={16} aria-hidden />
          <input
            ref={input}
            value={q}
            placeholder={tr("Go to a screen, a category, a country", "Aller à un écran, une catégorie, un pays")}
            onChange={(e) => {
              setQ(e.target.value);
              setAt(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") onClose();
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setAt((a) => Math.min(a + 1, shown.length - 1));
              }
              if (e.key === "ArrowUp") {
                e.preventDefault();
                setAt((a) => Math.max(a - 1, 0));
              }
              if (e.key === "Enter") pick(shown[at]);
            }}
            role="combobox"
            aria-expanded="true"
            aria-controls="palette-list"
            aria-activedescendant={shown[at]?.id}
          />
        </div>
        <ul id="palette-list" role="listbox">
          {shown.map((i, n) => (
            <li key={i.id} id={i.id} role="option" aria-selected={n === at} onMouseEnter={() => setAt(n)} onClick={() => pick(i)}>
              <span className="palette-kind">{KIND_LABELS[i.kind]}</span>
              <span className="palette-label">{i.label}</span>
              {i.hint && <span className="palette-hint">{i.hint}</span>}
            </li>
          ))}
          {shown.length === 0 && <li className="palette-none">{tr(`Nothing matches “${q}”.`, `Rien ne correspond à « ${q} ».`)}</li>}
        </ul>
      </div>
    </div>
  );
}
