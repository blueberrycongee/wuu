const bounds = [[0, 59], [0, 23], [1, 31], [1, 12], [0, 7]] as const;

function integer(value: string): number {
  return /^[+-]?\d+$/.test(value) ? Number(value) : NaN;
}

function validField(field: string, index: number): boolean {
  if (field === "*" || field === "?") return true;
  const [min, max] = bounds[index];
  return field.split(",").every((part) => {
    const pieces = part.trim().split("/");
    if (pieces.length > 2 || (pieces.length === 2 && !(integer(pieces[1]) > 0))) return false;
    const base = pieces[0];
    if (base === "*") return true;
    if (base.includes("-")) {
      const range = base.split("-");
      if (range.length !== 2) return false;
      const start = integer(range[0]), end = integer(range[1]);
      return start >= min && end <= max && start <= end;
    }
    const value = integer(base);
    return value >= min && value <= max;
  });
}

function match(field: string, value: number, min: number, max: number): boolean {
  if (field === "*" || field === "?") return true;
  return field.split(",").some((part) => {
    const [base, stride] = part.trim().split("/");
    const step = stride === undefined ? 1 : integer(stride);
    const [start, end] = base === "*" ? [min, max] : base.includes("-") ? base.split("-").map(integer) : [integer(base), integer(base)];
    return value >= start && value <= end && (value - start) % step === 0;
  });
}

export function localTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

function formatter(timezone: string): Intl.DateTimeFormat {
  return new Intl.DateTimeFormat("en-US", {
    timeZone: timezone === "Local" ? localTimezone() : timezone,
    year: "numeric", month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit", hourCycle: "h23",
  });
}

export function validateTimezone(timezone: string): void {
  try { formatter(timezone); }
  catch (error) { throw new Error(`invalid automation timezone ${JSON.stringify(timezone)}: ${String(error)}`); }
}

function wallTime(format: Intl.DateTimeFormat, timestamp: number): number {
  const values: Record<string, number> = {};
  for (const part of format.formatToParts(timestamp)) if (part.type !== "literal") values[part.type] = Number(part.value);
  const value = new Date(0);
  value.setUTCFullYear(values.year, values.month - 1, values.day);
  value.setUTCHours(values.hour, values.minute, 0, 0);
  return value.getTime();
}

export class CronExpression {
  constructor(readonly fields: readonly string[]) {}

  nextRun(after: Date, timezone: string): Date {
    const format = formatter(timezone);
    const start = Math.floor(after.getTime() / 60_000) * 60_000 + 60_000;
    const limit = new Date(start);
    limit.setUTCFullYear(limit.getUTCFullYear() + 4);
    const [minute, hour, dom, month, dow] = this.fields;
    const domStar = dom === "*" || dom === "?", dowStar = dow === "*" || dow === "?";
    let cachedHour = NaN, hourWall = 0, uniformHour = false;
    for (let timestamp = start; timestamp < limit.getTime(); timestamp += 60_000) {
      const utcHour = Math.floor(timestamp / 3_600_000) * 3_600_000;
      if (utcHour !== cachedHour) {
        cachedHour = utcHour;
        hourWall = wallTime(format, utcHour);
        // A transition can occur on a half-hour boundary. Only extrapolate an
        // hour when both endpoints prove its wall clock advances continuously.
        uniformHour = wallTime(format, utcHour + 59 * 60_000) - hourWall === 59 * 60_000;
      }
      const wall = new Date(uniformHour ? hourWall + timestamp - utcHour : wallTime(format, timestamp));
      if (!match(minute, wall.getUTCMinutes(), 0, 59) || !match(hour, wall.getUTCHours(), 0, 23) || !match(month, wall.getUTCMonth() + 1, 1, 12)) continue;
      const weekday = wall.getUTCDay();
      const domMatch = match(dom, wall.getUTCDate(), 1, 31);
      const dowMatch = match(dow, weekday, 0, 7) || (weekday === 0 && match(dow, 7, 0, 7));
      if ((!domStar && !dowStar) ? domMatch || dowMatch : domMatch && dowMatch) return new Date(timestamp);
    }
    throw new Error("no next run found within 4 years");
  }
}

export function parseCronExpression(input: string): CronExpression {
  const fields = input.trim() === "" ? [] : input.trim().split(/\s+/);
  if (fields.length !== 5) throw new Error(`cron expression must have exactly 5 fields, got ${fields.length}`);
  for (let i = 0; i < fields.length; i++) if (!validField(fields[i], i)) throw new Error(`invalid cron field ${JSON.stringify(fields[i])} at position ${i}`);
  return new CronExpression(fields);
}

export function intervalToCron(input: string): string {
  const interval = input.trim().toLowerCase();
  if (!interval) throw new Error("empty interval");
  const unit = interval.slice(-1), raw = interval.slice(0, -1), n = integer(raw);
  if (!"mhds".includes(unit)) throw new Error(`invalid interval unit: ${JSON.stringify(unit)}`);
  if (!(n > 0)) throw new Error(`invalid interval number: ${JSON.stringify(raw)}`);
  if (unit === "s") return n < 60 ? "*/1 * * * *" : intervalToCron(`${Math.ceil(n / 60)}m`);
  if (unit === "m") return n <= 59 ? `*/${n} * * * *` : `0 */${Math.ceil(n / 60)} * * *`;
  if (unit === "h") return n <= 23 ? `0 */${n} * * *` : `0 0 */${Math.ceil(n / 24)} * *`;
  return `0 0 */${n} * *`;
}
