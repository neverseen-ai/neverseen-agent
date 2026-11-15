import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from "react";
import { CircleCheck, OctagonAlert, X } from "lucide-react";
import { tr } from "../i18n";

// ---------------------------------------------------------------- toasts

export interface Toast {
  id: number;
  tone: "ok" | "bad";
  text: string;
  detail?: string;
  action?: { label: string; run: () => void };
}

type Push = (t: Omit<Toast, "id">) => void;
const ToastContext = createContext<Push>(() => {});

export function useToast(): Push {
  return useContext(ToastContext);
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<Toast[]>([]);
  const next = useRef(1);

  const dismiss = useCallback((id: number) => setToasts((all) => all.filter((t) => t.id !== id)), []);
  const push = useCallback<Push>(
    (t) => {
      const id = next.current++;
      setToasts((all) => [...all.slice(-3), { ...t, id }]);
      // A refusal stays longer: it is the one that has to be read.
      setTimeout(() => dismiss(id), t.tone === "bad" ? 9000 : 5000);
    },
    [dismiss],
  );

  return (
    <ToastContext.Provider value={push}>
      {children}
      <div className="toasts" role="region" aria-label="Notifications" aria-live="polite">
        {toasts.map((t) => (
          <div key={t.id} className={`toast toast-${t.tone}`}>
            {t.tone === "ok" ? <CircleCheck size={16} aria-hidden /> : <OctagonAlert size={16} aria-hidden />}
            <div className="toast-body">
              <p>{t.text}</p>
              {t.detail && <p className="toast-detail">{t.detail}</p>}
            </div>
            {t.action && (
              <button
                type="button"
                className="btn btn-quiet"
                onClick={() => {
                  dismiss(t.id);
                  t.action!.run();
                }}
              >
                {t.action.label}
              </button>
            )}
            <button type="button" className="btn btn-quiet btn-icon" aria-label={tr("Dismiss", "Fermer")} onClick={() => dismiss(t.id)}>
              <X size={14} aria-hidden />
            </button>
          </div>
        ))}
      </div>
    </ToastContext.Provider>
  );
}

// ---------------------------------------------------------------- dialog

/** A modal on the platform's own <dialog>: focus trapping, Escape and the backdrop for free. */
export function Dialog({
  open,
  onClose,
  title,
  children,
  footer,
  wide,
  className,
}: {
  open: boolean;
  onClose: () => void;
  title: string;
  children: ReactNode;
  footer?: ReactNode;
  wide?: boolean;
  className?: string;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);

  return (
    <dialog
      ref={ref}
      className={`dialog ${wide ? "dialog-wide" : ""} ${className ?? ""}`}
      onClose={onClose}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
      onClick={(e) => {
        if (e.target === ref.current) onClose();
      }}
      aria-label={title}
    >
      {open && (
        <div className="dialog-inner">
          <header className="dialog-head">
            <h2>{title}</h2>
            <button type="button" className="btn btn-quiet btn-icon" aria-label={tr("Close", "Fermer")} onClick={onClose}>
              <X size={16} aria-hidden />
            </button>
          </header>
          <div className="dialog-body">{children}</div>
          {footer && <footer className="dialog-foot">{footer}</footer>}
        </div>
      )}
    </dialog>
  );
}
