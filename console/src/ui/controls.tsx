import { useEffect, useRef, useState, type ReactNode } from "react";
import { Check, Copy, Info, TriangleAlert, OctagonAlert } from "lucide-react";
import { tr } from "../i18n";

export function Switch({
  on,
  onChange,
  label,
  disabled,
}: {
  on: boolean;
  onChange: (next: boolean) => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <button
      type="button"
      role="switch"
      className="switch"
      aria-checked={on}
      aria-label={label}
      disabled={disabled}
      onClick={() => onChange(!on)}
    />
  );
}

/** A checkbox that can say "some": a section with some switches off is neither on nor off. */
export function TriCheck({
  checked,
  indeterminate,
  disabled,
  onChange,
  children,
}: {
  checked: boolean;
  indeterminate: boolean;
  disabled?: boolean;
  onChange: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLInputElement>(null);
  // On every render, not when the prop changes: a click clears the browser's own flag,
  // and after a refused change the prop is what it was, so the box drew as clear while
  // aria-checked still said mixed.
  useEffect(() => {
    if (ref.current) ref.current.indeterminate = indeterminate;
  });
  return (
    <label className={`tri ${checked ? "" : "tri-off"}`}>
      <input
        ref={ref}
        type="checkbox"
        checked={checked}
        disabled={disabled}
        aria-checked={indeterminate ? "mixed" : checked}
        onChange={onChange}
      />
      <span>{children}</span>
    </label>
  );
}

async function writeClipboard(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // file:// and some webviews refuse the async clipboard; the old path still works.
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    const ok = document.execCommand("copy");
    area.remove();
    return ok;
  }
}

export function CopyButton({ text, label = tr("Copy", "Copier"), compact }: { text: string; label?: string; compact?: boolean }) {
  const [done, setDone] = useState(false);
  useEffect(() => {
    if (!done) return;
    const t = setTimeout(() => setDone(false), 1600);
    return () => clearTimeout(t);
  }, [done]);
  return (
    <button
      type="button"
      className={`btn btn-quiet ${compact ? "btn-icon" : ""}`}
      onClick={async () => setDone(await writeClipboard(text))}
      aria-label={compact ? label : undefined}
      title={compact ? label : undefined}
    >
      {done ? <Check size={14} aria-hidden /> : <Copy size={14} aria-hidden />}
      {!compact && <span>{done ? tr("Copied", "Copié") : label}</span>}
      <span className="sr-live" aria-live="polite">
        {done ? tr("Copied to the clipboard", "Copié dans le presse-papiers") : ""}
      </span>
    </button>
  );
}

/** One line somebody pastes into a terminal, with its copy button. */
export function CommandLine({ text, label }: { text: string; label?: string }) {
  return (
    <div className="cmd">
      <code>{text}</code>
      <CopyButton text={text} label={label} compact />
    </div>
  );
}

export function Notice({
  tone,
  title,
  children,
  action,
}: {
  tone: "info" | "warn" | "bad";
  title: ReactNode;
  children?: ReactNode;
  action?: ReactNode;
}) {
  const Icon = tone === "info" ? Info : tone === "warn" ? TriangleAlert : OctagonAlert;
  return (
    <div className={`notice notice-${tone}`} role={tone === "info" ? "note" : "status"}>
      <Icon size={16} className="notice-icon" aria-hidden />
      <div className="notice-body">
        <p className="notice-title">{title}</p>
        {children && <div className="notice-text">{children}</div>}
      </div>
      {action && <div className="notice-action">{action}</div>}
    </div>
  );
}

export function Empty({ icon, title, children, action }: { icon?: ReactNode; title: string; children?: ReactNode; action?: ReactNode }) {
  return (
    <div className="empty">
      {icon && <div className="empty-icon">{icon}</div>}
      <p className="empty-title">{title}</p>
      {children && <div className="empty-text">{children}</div>}
      {action && <div className="empty-action">{action}</div>}
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="kbd">{children}</kbd>;
}

export const MOD = typeof navigator !== "undefined" && /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘" : "Ctrl";
