export type SegmentedOption<T extends string> = {
  value: T;
  label: string;
  testID?: string;
};

/** One choice among a few short, mutually exclusive options. An undefined
 * value presses none of them, for a state the options do not name. */
export function SegmentedControl<T extends string>({ label, value, options, onChange, disabled, className }: {
  label: string;
  value: T | undefined;
  options: readonly SegmentedOption<T>[];
  onChange: (value: T) => void;
  disabled?: boolean;
  className?: string;
}): JSX.Element {
  return (
    <div className={className ? `theme-segmented ${className}` : "theme-segmented"} role="group" aria-label={label}>
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={value === option.value}
          disabled={disabled}
          data-testid={option.testID}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}
