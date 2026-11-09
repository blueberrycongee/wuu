// Small display formatters for the conversation list and thread view.

/** WeChat-style timestamp: time today, 昨天, weekday within a week, else date. */
export function formatListTimestamp(iso: string | undefined, now: Date = new Date()): string {
  if (!iso) return "";
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return "";
  const then = new Date(ms);
  const startOfDay = (d: Date) => new Date(d.getFullYear(), d.getMonth(), d.getDate()).getTime();
  const dayDiff = Math.round((startOfDay(now) - startOfDay(then)) / 86_400_000);
  const two = (n: number) => String(n).padStart(2, "0");
  if (dayDiff <= 0) return `${two(then.getHours())}:${two(then.getMinutes())}`;
  if (dayDiff === 1) return "昨天";
  if (dayDiff < 7) return ["周日", "周一", "周二", "周三", "周四", "周五", "周六"][then.getDay()];
  if (then.getFullYear() === now.getFullYear()) return `${then.getMonth() + 1}月${then.getDate()}日`;
  return `${then.getFullYear()}/${then.getMonth() + 1}/${then.getDate()}`;
}
