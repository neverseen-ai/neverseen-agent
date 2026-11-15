import { useId } from "react";

// The brand mark in its four states, drawn from the same numbers as
// internal/tray/icons/generate.go, in the same 24-unit space and the same crop.
//
// The left square is a value in clear, the right one that value replaced: outlined
// while it is being replaced, half filled when some of the catalogue is switched off,
// filled when nothing is. With no agent running the divider goes — it is the agent
// standing between a value and where it was going — which is what keeps "not
// running" from looking like "running and replacing nothing".

export type MarkState = "masking" | "partial" | "unmasked" | "absent";

export function Mark({ state, width = 20, label }: { state: MarkState; width?: number; label?: string }) {
  const clip = `half-${useId().replace(/[^a-zA-Z0-9]/g, "")}`;
  return (
    <svg
      className={`mark mark-${state}`}
      viewBox="4 6 16.8 12"
      width={width}
      height={(width * 12) / 16.8}
      role={label ? "img" : undefined}
      aria-hidden={label ? undefined : true}
      aria-label={label}
    >
      <defs>
        <clipPath id={clip}>
          <rect x="13.75" y="9.25" width="5.5" height="5.5" rx="1.25" />
        </clipPath>
      </defs>
      <line className="mark-divider" x1="12" y1="7.25" x2="12" y2="16.75" />
      <rect className="mark-left" x="4.75" y="9.25" width="5.5" height="5.5" rx="1.25" />
      <rect className="mark-right" x="13.75" y="9.25" width="5.5" height="5.5" rx="1.25" />
      <rect className="mark-half" x="13.75" y="9.25" width="2.75" height="5.5" clipPath={`url(#${clip})`} />
    </svg>
  );
}

/** A category's own state in the same vocabulary: outlined is masked, filled is in clear. */
export function Pip({ clear, title }: { clear: boolean; title?: string }) {
  return <span className={`pip ${clear ? "pip-clear" : "pip-masked"}`} title={title} aria-hidden="true" />;
}
