// Two languages, each sentence written beside its translation at the place it is
// drawn. No key file: a key names a sentence nobody can read without opening a
// second file, and the sentences here are the product — each says what a choice costs.
//
// TODO: two languages is the ceiling of tr(en, fr). A third wants a table per
// language and a key per sentence.

import { useSyncExternalStore } from "react";

export type Lang = "en" | "fr";
export const LANGS: { code: Lang; label: string }[] = [
  { code: "en", label: "English" },
  { code: "fr", label: "Français" },
];

const KEY = "neverseen.lang";

function read(): Lang {
  // ?lang=fr first, so a link — or a headless screenshot — picks its language.
  const fromUrl = new URLSearchParams(location.hash.split("?")[1] ?? "").get("lang");
  if (fromUrl === "en" || fromUrl === "fr") return fromUrl;
  try {
    const stored = localStorage.getItem(KEY);
    if (stored === "en" || stored === "fr") return stored;
  } catch {
    // Storage can be refused (a private window, some file:// origins): follow the system.
  }
  return navigator.language.toLowerCase().startsWith("fr") ? "fr" : "en";
}

let current: Lang = read();
document.documentElement.lang = current;
const listeners = new Set<() => void>();

export function lang(): Lang {
  return current;
}

export function setLang(next: Lang) {
  if (next === current) return;
  current = next;
  document.documentElement.lang = next;
  try {
    localStorage.setItem(KEY, next);
  } catch {
    // Not remembered, still applied.
  }
  for (const l of listeners) l();
}

export function onLang(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

/** Subscribes a component to the language: the App calls it, and everything under it redraws. */
export function useLang(): Lang {
  return useSyncExternalStore(onLang, lang);
}

/** One sentence in the language in use. */
export function tr<T>(en: T, fr: T): T {
  return current === "fr" ? fr : en;
}

export function locale(): string {
  return current === "fr" ? "fr-FR" : "en-GB";
}

export function num(n: number): string {
  return n.toLocaleString(locale());
}

// ---------------------------------------------------------------- what the agent serves

// The catalogue's labels and notations are the agent's answer, not the console's:
// the agent would be asked for a language and serve these. The client applies this
// at the one place it hands a label over, so no screen translates a served name.
//
// English on the left, French on the right. The last block is the notations the
// catalogue carries in French today, which an English console shows in English.
const SERVED: [en: string, fr: string][] = [
  ["Personal details", "Informations personnelles"],
  ["Company identifiers", "Identifiants d'entreprise"],
  ["Technical identifiers", "Identifiants techniques"],
  ["Banking", "Banque"],
  ["Connection strings", "Chaînes de connexion"],
  ["Secrets and keys", "Secrets et clés"],

  ["Postal address", "Adresse postale"],
  ["Date of birth", "Date de naissance"],
  ["Email address", "Adresse e-mail"],
  ["Geographic point", "Point géographique"],
  ["Vehicle registration", "Immatriculation de véhicule"],
  ["NHS number (gb)", "Numéro NHS (gb)"],
  ["National Insurance number (gb)", "Numéro National Insurance (gb)"],
  ["Social security number (fr)", "Numéro de sécurité sociale (fr)"],
  ["Telephone", "Téléphone"],
  ["Postcode", "Code postal"],
  ["Social security number (us)", "Numéro de sécurité sociale (us)"],
  ["Employer identification number (us)", "Numéro d'identification d'employeur (us)"],
  ["IPv6 address", "Adresse IPv6"],
  ["IPv4 address", "Adresse IPv4"],
  ["Database identifier", "Identifiant de base de données"],
  ["Payment card", "Carte de paiement"],
  ["Bank account (IBAN)", "Compte bancaire (IBAN)"],
  ["Bank routing number (us)", "Numéro de routage bancaire (us)"],
  ["Connection string", "Chaîne de connexion"],
  ["Secret in an assignment", "Secret dans une affectation"],

  ["UK street address", "Adresse britannique"],
  ["US street address", "Adresse américaine"],
  ["Date (month first)", "Date (mois d'abord)"],
  ["geo: URI", "URI geo:"],
  ["Google Maps link", "Lien Google Maps"],
  ["Apple Maps link", "Lien Apple Maps"],
  ["OpenStreetMap link", "Lien OpenStreetMap"],
  ["WKT point", "Point WKT"],
  ["Degrees, minutes, seconds", "Degrés, minutes, secondes"],
  ["what3words address", "Adresse what3words"],
  ["Registration mark (2001)", "Plaque britannique (2001)"],
  ["NHS number", "Numéro NHS"],
  ["National Insurance number", "Numéro National Insurance"],
  ["UK telephone number", "Numéro de téléphone britannique"],
  ["US telephone number", "Numéro de téléphone américain"],
  ["UK postcode", "Code postal britannique"],
  ["US ZIP code", "Code ZIP américain"],
  ["US Social Security number", "Numéro de sécurité sociale américain"],
  ["US employer identification number", "Numéro d'identification d'employeur américain"],
  ["ABA routing number", "Numéro de routage ABA"],
  ["Payment card number", "Numéro de carte de paiement"],
  ["Named secret or password", "Secret ou mot de passe nommé"],
  ["Named secret in an XML element", "Secret nommé dans un élément XML"],
  ["NuGet feed password", "Mot de passe de flux NuGet"],
  ["Authorization header credential", "Identifiant d'en-tête Authorization"],
  ["Session token", "Jeton de session"],
  ["URL carrying credentials (scheme://user:password@…)", "URL contenant des identifiants (scheme://user:password@…)"],
  [
    'codex does not read this: put openai_base_url = "<the URL above>" in ~/.codex/config.toml instead (user-level — the key is ignored in a project file)',
    'codex ne lit pas cette variable : mettez plutôt openai_base_url = "<l\'URL ci-dessus>" dans ~/.codex/config.toml (niveau utilisateur — la clé est ignorée dans un fichier de projet)',
  ],
  ["PEM private key, whole block", "Clé privée PEM, bloc entier"],
  ["PEM private key header", "En-tête de clé privée PEM"],
  ["Slack webhook URL", "URL de webhook Slack"],
  ["AWS access key id", "Identifiant de clé d'accès AWS"],
  ["AWS secret access key (beside its key id)", "Clé d'accès secrète AWS (à côté de son identifiant)"],
  ["OpenAI API key (legacy)", "Clé API OpenAI (ancien format)"],

  ["French street address", "Adresse française"],
  ["Date (day first)", "Date (jour d'abord)"],
  ["Registration plate (SIV)", "Plaque d'immatriculation (SIV)"],
  ["Social security number (NIR)", "Numéro de sécurité sociale (NIR)"],
  ["French telephone number", "Numéro de téléphone français"],
  ["Postcode and town", "Code postal et commune"],
];
const TO_FR = new Map(SERVED);
const TO_EN = new Map(SERVED.map(([en, fr]) => [fr, en]));

// The hundred and thirty vendor entries follow three shapes, so they are rewritten
// rather than listed: a list of our own would fall behind the catalogue.
const VENDOR_SHAPES: [RegExp, string][] = [
  [/^Registration plate \(([a-z]{2})\)$/, "Plaque d'immatriculation ($1)"],
  [/^(.+) credential( \(.*\))?$/, "Identifiant $1$2"],
  [/^(.+) API key$/, "Clé API $1"],
  [/^(.+) key$/, "Clé $1"],
  [/^(.+) token$/, "Jeton $1"],
];

/** A label or a notation as the agent would serve it in the language in use. */
export function served(text: string): string {
  if (current === "en") return TO_EN.get(text) ?? text;
  const known = TO_FR.get(text);
  if (known) return known;
  for (const [shape, to] of VENDOR_SHAPES) if (shape.test(text)) return text.replace(shape, to);
  return text;
}
