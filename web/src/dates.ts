// Civil dates are "YYYY-MM-DD" strings throughout; "" means none.

/** Today in the given IANA time zone. */
export function today(tz?: string): string {
  try {
    return new Intl.DateTimeFormat("en-CA", { timeZone: tz || undefined }).format(new Date());
  } catch {
    return new Intl.DateTimeFormat("en-CA").format(new Date());
  }
}

export function browserTimeZone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone;
}

function toUTC(d: string): number {
  const [y, m, day] = d.split("-").map(Number);
  return Date.UTC(y, m - 1, day);
}

/** Days from a to b (negative if b is earlier). */
export function daysBetween(a: string, b: string): number {
  return Math.round((toUTC(b) - toUTC(a)) / 86_400_000);
}

export function addDays(d: string, n: number): string {
  return new Date(toUTC(d) + n * 86_400_000).toISOString().slice(0, 10);
}

/** "Tue, Oct 6", with the year if it isn't this year's. */
export function formatDate(d: string, ref = today()): string {
  if (!d) return "";
  const sameYear = d.slice(0, 4) === ref.slice(0, 4);
  return new Date(toUTC(d)).toLocaleDateString(undefined, {
    timeZone: "UTC",
    weekday: "short",
    month: "short",
    day: "numeric",
    year: sameYear ? undefined : "numeric",
  });
}

/** "today", "tomorrow", "in 5 days", "3 days overdue", "in 2 months". */
export function relativeDue(due: string, ref: string): string {
  const n = daysBetween(ref, due);
  if (n === 0) return "today";
  if (n === 1) return "tomorrow";
  if (n === -1) return "yesterday";
  const abs = Math.abs(n);
  const span =
    abs < 14 ? `${abs} days` : abs < 60 ? `${Math.round(abs / 7)} weeks` : abs < 730 ? `${Math.round(abs / 30)} months` : `${Math.round(abs / 365)} years`;
  return n > 0 ? `in ${span}` : `${span} overdue`;
}

/** "today", "yesterday", "5 days ago", "3 weeks ago", "4 months ago", "2 years ago". */
export function relativePast(d: string, ref: string): string {
  const n = daysBetween(d, ref);
  if (n <= 0) return "today";
  if (n === 1) return "yesterday";
  const plural = (k: number, unit: string) => `${k} ${unit}${k === 1 ? "" : "s"} ago`;
  if (n < 14) return plural(n, "day");
  if (n < 60) return plural(Math.round(n / 7), "week");
  if (n < 730) return plural(Math.round(n / 30), "month");
  return plural(Math.round(n / 365), "year");
}

export function formatTimestamp(ts: { seconds: bigint } | undefined): string {
  if (!ts) return "";
  return new Date(Number(ts.seconds) * 1000).toLocaleString(undefined, {
    month: "short",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  });
}
