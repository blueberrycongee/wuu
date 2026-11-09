// Activity timestamps come from the Go core as UTC RFC3339Nano. Pad omitted
// fractional digits so lexical comparisons preserve the full control ordering.
export function activityTimestamp(value: string): string {
  return value.replace(/(?:\.(\d+))?Z$/, (_match, fraction: string | undefined) => `.${(fraction ?? "").padEnd(9, "0")}Z`);
}
