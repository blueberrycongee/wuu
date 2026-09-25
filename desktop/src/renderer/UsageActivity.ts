import type { SettingsUsageDay } from "../shared/protocol";

// Daily usage series shared by the settings usage page and the empty
// conversation home. Server days are calendar date keys; these helpers lay
// them onto the local calendar that ends today.

export type UsageHeatmapCell = SettingsUsageDay & {
  level: number;
};

export function formatCompactUsageNumber(value: number, locale: string): string {
  if (!Number.isFinite(value)) {
    return "—";
  }
  const units = [
    { threshold: 1_000, suffix: "k" },
    { threshold: 1_000_000, suffix: "M" },
    { threshold: 1_000_000_000, suffix: "B" },
  ];
  const absoluteValue = Math.abs(value);
  if (absoluteValue < units[0].threshold) {
    return new Intl.NumberFormat(locale).format(value);
  }

  let unitIndex = 0;
  while (unitIndex < units.length - 1 && absoluteValue >= units[unitIndex + 1].threshold) {
    unitIndex += 1;
  }

  let scaled = value / units[unitIndex].threshold;
  if (Math.abs(Math.round(scaled * 10) / 10) >= 1_000 && unitIndex < units.length - 1) {
    unitIndex += 1;
    scaled = value / units[unitIndex].threshold;
  }

  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 1 }).format(scaled)}${units[unitIndex].suffix}`;
}

/** One cell per local day from the Sunday a year back through today. */
export function buildUsageHeatmap(days: SettingsUsageDay[]): UsageHeatmapCell[] {
  const byDate = new Map(days.map((day) => [day.date, day]));
  const end = startOfLocalDay(new Date());
  const start = startOfWeek(addDays(end, -364));
  const startKey = localDateKey(start);
  const endKey = localDateKey(end);
  const activeTotals = days
    .filter((day) => day.date >= startKey && day.date <= endKey)
    .map(usageTokenTotal)
    .filter((total) => total > 0)
    .sort((a, b) => a - b);
  const cells: UsageHeatmapCell[] = [];
  for (let cursor = start; cursor.getTime() <= end.getTime(); cursor = addDays(cursor, 1)) {
    const date = localDateKey(cursor);
    const day = byDate.get(date) ?? emptyUsageDay(date);
    cells.push({
      ...day,
      level: usageHeatmapLevel(day, activeTotals),
    });
  }
  return cells;
}

function usageHeatmapLevel(day: SettingsUsageDay, activeTotals: number[]): number {
  const total = usageTokenTotal(day);
  if (total <= 0 || activeTotals.length === 0) {
    return 0;
  }
  const upperRank = activeTotals.findLastIndex((candidate) => candidate <= total) + 1;
  return Math.min(4, Math.max(1, Math.ceil((upperRank / activeTotals.length) * 4)));
}

export function usageTokenTotal(day: SettingsUsageDay): number {
  return (
    Math.max(0, day.input_tokens) +
    Math.max(0, day.output_tokens) +
    Math.max(0, day.cache_creation_tokens) +
    Math.max(0, day.cache_read_tokens)
  );
}

export function buildUsageTrend(days: SettingsUsageDay[], length = 30): SettingsUsageDay[] {
  const byDate = new Map(days.map((day) => [day.date, day]));
  const end = startOfLocalDay(new Date());
  return Array.from({ length }, (_, index) => {
    const date = localDateKey(addDays(end, index - length + 1));
    return byDate.get(date) ?? emptyUsageDay(date);
  });
}

function emptyUsageDay(date: string): SettingsUsageDay {
  return {
    date,
    input_tokens: 0,
    output_tokens: 0,
    cache_creation_tokens: 0,
    cache_read_tokens: 0,
    cache_hit_rate: 0,
    turns: 0,
    agents: 0,
  };
}

function startOfLocalDay(date: Date): Date {
  return new Date(date.getFullYear(), date.getMonth(), date.getDate());
}

function startOfWeek(date: Date): Date {
  const day = date.getDay();
  return addDays(startOfLocalDay(date), -day);
}

function addDays(date: Date, days: number): Date {
  const out = new Date(date);
  out.setDate(out.getDate() + days);
  return out;
}

function localDateKey(date: Date): string {
  const year = date.getFullYear();
  const month = `${date.getMonth() + 1}`.padStart(2, "0");
  const day = `${date.getDate()}`.padStart(2, "0");
  return `${year}-${month}-${day}`;
}
