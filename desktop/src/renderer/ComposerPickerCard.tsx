import { Check, Search } from "lucide-react";
import type { ReactNode } from "react";

/* The composer workspace bar opens two pickers — the project card and the
 * branch card — from two feature modules. Both render through this shell so
 * one row anatomy, one search row, one focus entry, and one Escape behaviour
 * describe both cards. Writing the markup twice let them drift: they ended up
 * with different row contents, different keyboard dismissal, and different
 * internal scroll limits.
 *
 * Row anatomy is fixed: leading icon, one label, optional trailing check. A
 * row that represents a choice is a menuitemradio in both cards, so the
 * current item is announced as checked; a row that runs an action stays a
 * plain menuitem. The picker decides for itself whether its current item is
 * still actionable by passing `disabled`. */

export function ComposerPickerCard({ label, busy, onDismiss, children }: {
  label: string;
  busy?: boolean;
  onDismiss?: () => void;
  children: ReactNode;
}): JSX.Element {
  return (
    <div
      className="composer-project-menu"
      role="menu"
      aria-label={label}
      aria-busy={busy}
      onKeyDown={onDismiss ? (event) => {
        if (event.key !== "Escape" || event.repeat) {
          return;
        }
        // The card is portaled, so React still bubbles this event to a menu
        // toggle above it. Stop here or the dismissal reopens the card.
        event.preventDefault();
        event.stopPropagation();
        onDismiss();
      } : undefined}
    >
      {children}
    </div>
  );
}

/* The placeholder doubles as the accessible name: both pickers search their
 * own list by substring, so the same wording describes the field and the
 * control. */
export function ComposerPickerSearch({ label, value, onChange }: {
  label: string;
  value: string;
  onChange: (value: string) => void;
}): JSX.Element {
  return (
    <label className="menu-search">
      <Search className="icon-sm" aria-hidden="true" />
      <input autoFocus value={value} aria-label={label} placeholder={label}
        onChange={(event) => onChange(event.target.value)} />
    </label>
  );
}

export function ComposerPickerList({ empty, emptyMessage, children }: {
  empty: boolean;
  emptyMessage: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <div className="project-picker-list">
      {empty ? <div className="project-picker-empty">{emptyMessage}</div> : null}
      {children}
    </div>
  );
}

export function ComposerPickerRow({ icon, label, title, selected, disabled, onSelect }: {
  icon: ReactNode;
  label: string;
  title?: string;
  selected?: boolean;
  disabled?: boolean;
  onSelect: () => void;
}): JSX.Element {
  const choice = selected !== undefined;
  return (
    <button
      type="button"
      role={choice ? "menuitemradio" : "menuitem"}
      aria-checked={choice ? selected : undefined}
      title={title}
      disabled={disabled}
      onClick={onSelect}
    >
      {icon}
      <span>{label}</span>
      {selected ? <Check /> : null}
    </button>
  );
}
