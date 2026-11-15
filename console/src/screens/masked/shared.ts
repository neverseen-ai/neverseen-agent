import type { HealthCategory } from "../../agent/types";

/** Case-insensitive match over what a reader would search for: the name, the code,
 * and the shapes the category is recognised by. */
export function matches(q: string, ...fields: (string | undefined)[]): boolean {
  const needle = q.trim().toLowerCase();
  if (!needle) return true;
  return fields.some((f) => f?.toLowerCase().includes(needle));
}

export function categoryMatches(q: string, c: HealthCategory): boolean {
  return matches(q, c.label, c.code, ...(c.notations ?? []));
}

// The one category the secret level grades (Detector.strongEnough). Everything else on
// the credentials tab is recognised by its shape and masked at every level.
export const GRADED = "SECRET_GENERIC";

export const LEVEL_RANK: Record<string, number> = { weak: 0, medium: 1, strong: 2 };
