import type { ButtonHTMLAttributes } from "react";

type SwitchProps = Omit<ButtonHTMLAttributes<HTMLButtonElement>, "children" | "role" | "type"> & {
  checked: boolean;
  /** Visually hidden name, for switches whose name is not supplied through
   * `aria-label` or `aria-labelledby`. */
  label?: string;
};

/** The host's on/off control; `aria-checked` carries the state. */
export function Switch({ checked, label, className, ...props }: SwitchProps): JSX.Element {
  return (
    <button
      {...props}
      type="button"
      role="switch"
      aria-checked={checked}
      className={className ? `settings-switch ${className}` : "settings-switch"}
    >
      <span className="settings-switch-thumb" aria-hidden="true" />
      {label ? <span className="sr-only">{label}</span> : null}
    </button>
  );
}
