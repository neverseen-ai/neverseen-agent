import { Search } from "lucide-react";
import { parseRoute, routeHash, useStore } from "../app/store";
import { splitGroups } from "../agent/decisions";
import { countryName } from "../copy";
import { tr } from "../i18n";
import { Empty, Kbd } from "../ui/controls";
import { Mark } from "../ui/Mark";
import { Countries } from "./masked/Countries";
import { Credentials } from "./masked/Credentials";
import { Personal } from "./masked/Personal";
import { categoryMatches, GRADED, matches } from "./masked/shared";
import { Substitution } from "./masked/Substitution";

type Tab = "personal" | "credentials" | "substitution";
const TABS: { id: Tab; label: () => string }[] = [
  { id: "personal", label: () => tr("Personal data", "Données personnelles") },
  { id: "credentials", label: () => tr("Credentials", "Secrets") },
  { id: "substitution", label: () => tr("Substitution", "Substitution") },
];

// Three tabs, as on the settings page and for its reason: scrolled together, the
// hundred and thirty-odd credentials sat between two settings.
export function Masked() {
  const { snap, verdict, route, go } = useStore();
  const tab = (TABS.some((t) => t.id === route.params.get("tab")) ? route.params.get("tab") : "personal") as Tab;
  const q = route.params.get("q") ?? "";
  const setQ = (next: string) => {
    // The theme and the language ride along, as they do through go().
    const current = parseRoute(location.hash).params;
    const hash = routeHash("masked", {
      tab: tab !== "personal" ? tab : undefined,
      q: next || undefined,
      theme: current.get("theme") ?? undefined,
      lang: current.get("lang") ?? undefined,
    });
    history.replaceState(null, "", hash);
    dispatchEvent(new HashChangeEvent("hashchange"));
  };

  if (!snap) return null;
  if (verdict === "stopped") {
    return (
      <div className="page">
        <PageHead />
        <Empty
          icon={<Mark state="absent" width={56} />}
          title={tr("The agent is stopped", "L'agent est arrêté")}
        >
          {tr(
            <>
              This screen shows what the agent can recognise, and only the running agent can say. Start it again with{" "}
              <code>neverseen service restart</code>; this page follows as soon as it answers.
            </>,
            <>
              Cet écran montre ce que l'agent sait reconnaître, et seul l'agent en marche peut le dire. Relancez-le avec{" "}
              <code>neverseen service restart</code> : cette page suit dès qu'il répond.
            </>,
          )}
        </Empty>
      </div>
    );
  }

  const h = snap.health;
  const { personal, credentials } = splitGroups(h.groups);
  // Counted as each tab lists: a family whose name matches lists all of its categories,
  // and the graded catch-all is never in a credentials list.
  const hits: Record<Tab, number> = {
    personal:
      personal.flatMap((g) => g.categories.filter((c) => categoryMatches(q, c) || categoryMatches(q, { code: g.code, label: g.label }))).length +
      (h.available_locales ?? []).filter((l) => matches(q, l, countryName(l), ...(h.locale_categories?.[l] ?? []))).length,
    credentials: credentials.flatMap((g) => g.categories).filter((c) => c.code !== GRADED && categoryMatches(q, c)).length,
    substitution: 0,
  };

  return (
    <div className="page">
      <PageHead>
        <div className="search">
          <Search size={14} aria-hidden />
          <input
            id="search"
            type="search"
            placeholder={tr("Find a category, a country or a prefix", "Chercher une catégorie, un pays ou un préfixe")}
            value={q}
            onChange={(e) => setQ(e.target.value)}
            aria-label={tr("Find a category, a country or a prefix", "Chercher une catégorie, un pays ou un préfixe")}
          />
          {!q && <Kbd>/</Kbd>}
        </div>
      </PageHead>

      <div className="tabs" role="tablist" aria-label={tr("What is masked", "Ce qui est masqué")}>
        {TABS.map((t) => (
          <button
            key={t.id}
            type="button"
            role="tab"
            aria-selected={tab === t.id}
            className="tab"
            onClick={() => go("masked", { tab: t.id === "personal" ? undefined : t.id, q: q || undefined })}
          >
            {t.label()}
            {q && t.id !== "substitution" && <span className="tab-count">{hits[t.id]}</span>}
          </button>
        ))}
      </div>

      <div role="tabpanel">
        {tab === "personal" && (
          <>
            <Countries snap={snap} q={q} />
            <Personal snap={snap} q={q} />
          </>
        )}
        {tab === "credentials" && <Credentials snap={snap} q={q} />}
        {tab === "substitution" && <Substitution snap={snap} />}
      </div>
    </div>
  );
}

function PageHead({ children }: { children?: React.ReactNode }) {
  return (
    <header className="page-head">
      <div>
        <h1>{tr("What is masked", "Ce qui est masqué")}</h1>
        <p className="lede">
          {tr("Every change applies from the next request.", "Chaque changement s'applique dès la prochaine requête.")}
        </p>
      </div>
      {children}
    </header>
  );
}
