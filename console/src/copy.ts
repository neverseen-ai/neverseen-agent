// The sentences that say what a choice costs. Prose stays with the surface; *which*
// modes and levels exist is the agent's answer (Health.substitutions, secret_levels),
// and a value served without a sentence here is drawn under its bare name.
//
// Getters, so a sentence is read in the language in use at the moment it is drawn.

import { lang, locale, tr } from "./i18n";

export const MODES: Record<string, { name: string; plural: string; why: string; example: string }> = {
  token: {
    get name() {
      return tr("Placeholder", "Étiquette");
    },
    get plural() {
      return tr("placeholders", "étiquettes");
    },
    get why() {
      return tr(
        "Easy to spot, and impossible to mistake for real data. The real value is put back in the answer, on this computer.",
        "Se repère d'un coup d'œil, impossible à confondre avec une vraie donnée. La vraie valeur est remise dans la réponse, sur ce poste.",
      );
    },
    example: `[${"EMAIL"}_1]`, // assembled, so no surface expands it as a token
  },
  fake: {
    get name() {
      return tr("Stand-in", "Valeur fictive");
    },
    get plural() {
      return tr("stand-ins", "valeurs fictives");
    },
    get why() {
      return tr(
        "A believable fake: the provider reads natural text, and so do you, with nothing to show what was replaced. The real value is put back in the answer too.",
        "Une fausse valeur crédible : le fournisseur lit un texte naturel, vous aussi, sans rien qui montre ce qui a été remplacé. La vraie valeur est remise dans la réponse, elle aussi.",
      );
    },
    example: "contact1@example.org",
  },
};

export const LEVELS: Record<string, { name: string; why: string }> = {
  weak: {
    get name() {
      return tr("Weak", "Faible");
    },
    get why() {
      return tr("Masked even when it is a plain word. The safest.", "Masqué même si c'est un simple mot. Le plus sûr.");
    },
  },
  medium: {
    get name() {
      return tr("Medium", "Moyen");
    },
    get why() {
      return tr("Masked once it mixes two kinds of character.", "Masqué dès qu'il mêle deux sortes de caractères.");
    },
  },
  strong: {
    get name() {
      return tr("Strong", "Fort");
    },
    get why() {
      // pii.SecretStrength: three classes, or two over a value of 32 characters or more.
      return tr(
        "Masked from three kinds of character, or a long generated key.",
        "Masqué à partir de trois sortes de caractères, ou d'une longue clé générée.",
      );
    },
  },
};

export function licence(): string {
  return tr(
    "Source available today, open source on a two-year delay.",
    "Source disponible aujourd'hui, open source avec un délai de deux ans.",
  );
}

/** A country code as a name, from the platform rather than a list of our own. */
const regions = new Map<string, Intl.DisplayNames | undefined>();
function regionNames(): Intl.DisplayNames | undefined {
  if (!regions.has(lang())) {
    try {
      regions.set(lang(), new Intl.DisplayNames([locale()], { type: "region" }));
    } catch {
      regions.set(lang(), undefined);
    }
  }
  return regions.get(lang());
}

export function countryName(code: string): string {
  try {
    return regionNames()?.of(code.toUpperCase()) ?? code;
  } catch {
    return code;
  }
}

export function plural(n: number, one: string, many: string): string {
  // French counts zero as one of something; English counts it as several.
  const single = lang() === "fr" ? n < 2 : n === 1;
  return `${n.toLocaleString(locale())} ${single ? one : many}`;
}

/** "Email address and IPv4 address", "A, B and C". */
export function listOf(items: string[]): string {
  if (items.length <= 1) return items.join("");
  return `${items.slice(0, -1).join(", ")} ${tr("and", "et")} ${items[items.length - 1]}`;
}

export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return tr("never", "jamais");
  const s = Math.max(0, Math.round((now - new Date(iso).getTime()) / 1000));
  if (s < 45) return tr("just now", "à l'instant");
  const m = Math.round(s / 60);
  if (m < 60) return tr(`${m} min ago`, `il y a ${m} min`);
  const h = Math.round(m / 60);
  if (h < 24) return tr(`${h} h ago`, `il y a ${h} h`);
  const d = Math.round(h / 24);
  return tr(`${d} day${d === 1 ? "" : "s"} ago`, `il y a ${d} jour${d === 1 ? "" : "s"}`);
}

export function since(iso: string | undefined, now = Date.now()): string {
  if (!iso) return "—";
  const m = Math.max(0, Math.round((now - new Date(iso).getTime()) / 60000));
  if (m < 60) return `${m} min`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} h ${m % 60} min`;
  return `${Math.floor(h / 24)} ${tr("d", "j")} ${h % 24} h`;
}

export function clock(iso: string | undefined): string {
  if (!iso) return "—";
  return new Date(iso).toLocaleString(locale(), { dateStyle: "medium", timeStyle: "short" });
}
