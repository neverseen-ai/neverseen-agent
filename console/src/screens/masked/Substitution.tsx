import { useState } from "react";
import { useStore } from "../../app/store";
import type { Snapshot } from "../../agent/types";
import { MODES } from "../../copy";
import { tr } from "../../i18n";
import { Notice } from "../../ui/controls";
import { Dialog } from "../../ui/overlays";

export function Substitution({ snap }: { snap: Snapshot }) {
  const { change, busy } = useStore();
  const h = snap.health;
  const [pending, setPending] = useState<string>();

  const confirm = async () => {
    const mode = pending!;
    setPending(undefined);
    await change(
      { substitution: mode },
      tr(`Values are now replaced with ${MODES[mode]?.plural ?? mode}`, `Les valeurs sont désormais remplacées par des ${MODES[mode]?.plural ?? mode}`),
      { detail: tr("Conversations in progress start naming their values afresh.", "Les conversations en cours renomment leurs valeurs à neuf.") },
    );
  };

  return (
    <section aria-labelledby="subst-title" className="block">
      <h2 id="subst-title">{tr("What a masked value becomes", "Ce que devient une valeur masquée")}</h2>
      <p className="lede">
        {tr(
          "What the provider receives in place of a personal value. Either way, the real value is put back in the answer. Secrets always get a placeholder: a fake key would look like a working one.",
          "Ce que le fournisseur reçoit à la place d'une donnée personnelle. Dans les deux cas, la vraie valeur est remise dans la réponse. Les secrets reçoivent toujours une étiquette : une fausse clé ressemblerait à une vraie.",
        )}
      </p>
      <Notice tone="warn" title={tr("Best changed between two conversations", "À changer de préférence entre deux conversations")}>
        {tr(
          `In a conversation under way, the model already knows each value by a name, such as ${MODES.token.example}. After the change the same value gets a new name, and whatever the model writes with the old one is no longer put back. No other setting does this.`,
          `Dans une conversation en cours, le modèle connaît déjà chaque valeur sous un nom, comme ${MODES.token.example}. Après le changement, la même valeur reçoit un autre nom, et ce que le modèle écrit avec l'ancien n'est plus remis en clair. Aucun autre réglage ne fait cela.`,
        )}
      </Notice>
      <div className="choices" role="radiogroup" aria-labelledby="subst-title">
        {(h.substitutions ?? []).map((mode) => {
          const live = mode === h.substitution;
          return (
            <label key={mode} className={`choice ${live ? "choice-on" : ""}`}>
              <input type="radio" name="substitution" checked={live} disabled={busy} onChange={() => setPending(mode)} />
              <span className="choice-name">{MODES[mode]?.name ?? mode}</span>
              <code className="choice-example">{MODES[mode]?.example}</code>
              <span className="choice-why">{MODES[mode]?.why ?? ""}</span>
            </label>
          );
        })}
      </div>

      <Dialog
        open={pending !== undefined}
        onClose={() => setPending(undefined)}
        title={tr(`Switch to ${MODES[pending ?? ""]?.plural ?? pending}?`, `Passer aux ${MODES[pending ?? ""]?.plural ?? pending} ?`)}
        footer={
          <>
            <button type="button" className="btn" onClick={() => setPending(undefined)}>
              {tr("Keep", "Garder les")} {MODES[h.substitution]?.plural ?? h.substitution}
            </button>
            <button type="button" className="btn btn-primary" onClick={() => void confirm()}>
              {tr("Switch and clear", "Changer et effacer")}
            </button>
          </>
        }
      >
        <p>
          {tr(
            "This clears the replacements minted so far: conversations in progress start naming their values afresh.",
            "Cela efface les remplacements créés jusqu'ici : les conversations en cours renomment leurs valeurs à neuf.",
          )}{" "}
          {tr(
            "It cannot be undone: switching back mints new replacements, not the old ones.",
            "C'est irréversible : revenir en arrière crée de nouveaux remplacements, pas les anciens.",
          )}
        </p>
      </Dialog>
    </section>
  );
}
