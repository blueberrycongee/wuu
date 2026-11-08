import type { ReactNode } from "react";
import { useI18n } from "../i18n";

/**
 * A numeric field with explicit decrement and increment buttons. The child
 * is the field itself (it keeps its own typing, clamping, and persistence);
 * the buttons move the value by one step through `onStep`.
 */
export function SettingsStepper({
  onStep,
  atMin = false,
  atMax = false,
  disabled = false,
  children,
}: {
  onStep: (direction: -1 | 1) => void;
  atMin?: boolean;
  atMax?: boolean;
  disabled?: boolean;
  children: ReactNode;
}): JSX.Element {
  const { t } = useI18n();
  return (
    <div className="settings-stepper" data-wuu-component="settings-stepper">
      <button
        type="button"
        className="settings-stepper-button"
        aria-label={t("settings.stepDown")}
        disabled={disabled || atMin}
        onClick={() => onStep(-1)}
      >
        <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"><path d="M3.5 8h9" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></svg>
      </button>
      {children}
      <button
        type="button"
        className="settings-stepper-button"
        aria-label={t("settings.stepUp")}
        disabled={disabled || atMax}
        onClick={() => onStep(1)}
      >
        <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true"><path d="M8 3.5v9M3.5 8h9" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" /></svg>
      </button>
    </div>
  );
}
