import { DEFAULT_BASE_URL } from './agent.ts';

// What this extension keeps, and where.
//
// chrome.storage.local: readable by this extension in this browser profile and by
// nothing else — not by the page, not by another extension, not by another account on
// the machine. It is the profile's equivalent of the 0600 the agent gives the key
// file it reads this from.
//
// Two settings and no more. The key, which is pasted; and the agent's address, which
// is discovered rather than asked for — the manual setting exists for the one case
// the discovery cannot cover, an agent moved by NEVERSEEN_LISTEN.

export type Settings = {
  /** baseUrl is where the agent answers, with no trailing slash. */
  baseUrl: string;

  /** key is the agent's control secret, as `neverseen key` prints it. */
  key: string;
};

const STORAGE_KEY = 'neverseen';

/** Storage is the slice of chrome.storage.local this needs, so a test can pass a map. */
export type Storage = {
  get(keys: string): Promise<Record<string, unknown>>;
  set(items: Record<string, unknown>): Promise<void>;
};

export async function load(storage: Storage): Promise<Settings> {
  const stored = (await storage.get(STORAGE_KEY))[STORAGE_KEY] as Partial<Settings> | undefined;
  return {
    baseUrl: normalise(stored?.baseUrl) || DEFAULT_BASE_URL,
    key: (stored?.key ?? '').trim(),
  };
}

export async function save(storage: Storage, settings: Settings): Promise<void> {
  await storage.set({
    [STORAGE_KEY]: {
      baseUrl: normalise(settings.baseUrl) || DEFAULT_BASE_URL,
      key: settings.key.trim(),
    },
  });
}

/**
 * looksLikeAKey reports whether a pasted string is the shape the agent writes.
 *
 * Checked here as well as by the agent, and the reason is the message: a person who
 * pasted a line of the terminal's output around the key learns it from the page they
 * are looking at rather than from a 403 that reads as "the agent refused you".
 * proxy.trimKey applies the same rule for the same reason — a truncated secret is
 * worse than none, because it looks like protection.
 */
export function looksLikeAKey(value: string): boolean {
  return /^[0-9a-f]{64}$/.test(value.trim());
}

function normalise(url: string | undefined): string {
  return (url ?? '').trim().replace(/\/+$/, '');
}
