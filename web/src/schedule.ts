import { IntervalUnit, ScheduleKind, type Schedule } from "./api";

export const WEEKDAYS = ["MO", "TU", "WE", "TH", "FR", "SA", "SU"] as const;
export type Weekday = (typeof WEEKDAYS)[number];
const DAY_NAMES: Record<Weekday, string> = { MO: "Monday", TU: "Tuesday", WE: "Wednesday", TH: "Thursday", FR: "Friday", SA: "Saturday", SU: "Sunday" };
const SHORT_DAY: Record<Weekday, string> = { MO: "Mon", TU: "Tue", WE: "Wed", TH: "Thu", FR: "Fri", SA: "Sat", SU: "Sun" };
export const MONTHS = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];
export const ORDINALS: Record<number, string> = { 1: "first", 2: "second", 3: "third", 4: "fourth", [-1]: "last" };

export const UNIT_NAMES: Record<number, [string, string]> = {
  [IntervalUnit.DAYS]: ["day", "days"],
  [IntervalUnit.WEEKS]: ["week", "weeks"],
  [IntervalUnit.MONTHS]: ["month", "months"],
  [IntervalUnit.YEARS]: ["year", "years"],
};

/**
 * A calendar rule the builder understands. Anything else is kept as a raw
 * RRULE ("custom").
 */
export type Rule =
  | { mode: "weekly"; every: number; days: Weekday[] }
  | { mode: "monthlyDay"; every: number; day: number }
  | { mode: "monthlyWeekday"; every: number; ordinal: number; day: Weekday }
  | { mode: "yearly"; month: number; day: number }
  | { mode: "custom"; rrule: string };

function parts(rrule: string): Map<string, string> {
  const m = new Map<string, string>();
  for (const p of rrule.replace(/^RRULE:/i, "").split(";")) {
    const [k, v] = p.split("=");
    if (k && v !== undefined) m.set(k.toUpperCase(), v.toUpperCase());
  }
  return m;
}

export function parseRule(rrule: string): Rule {
  const p = parts(rrule);
  const every = Number(p.get("INTERVAL") ?? "1");
  const known = (...keys: string[]) => [...p.keys()].every((k) => ["FREQ", "INTERVAL", ...keys].includes(k));
  const freq = p.get("FREQ");
  if (freq === "WEEKLY" && known("BYDAY")) {
    const days = (p.get("BYDAY") ?? "").split(",").filter((d): d is Weekday => (WEEKDAYS as readonly string[]).includes(d));
    if (days.length) return { mode: "weekly", every, days };
  }
  if (freq === "MONTHLY" && known("BYMONTHDAY") && /^\d+$/.test(p.get("BYMONTHDAY") ?? "")) {
    return { mode: "monthlyDay", every, day: Number(p.get("BYMONTHDAY")) };
  }
  const m = /^(-?\d)(MO|TU|WE|TH|FR|SA|SU)$/.exec(p.get("BYDAY") ?? "");
  if (freq === "MONTHLY" && known("BYDAY") && m && ORDINALS[Number(m[1])]) {
    return { mode: "monthlyWeekday", every, ordinal: Number(m[1]), day: m[2] as Weekday };
  }
  if (freq === "YEARLY" && known("BYMONTH", "BYMONTHDAY") && every === 1 && p.has("BYMONTH") && p.has("BYMONTHDAY")) {
    return { mode: "yearly", month: Number(p.get("BYMONTH")), day: Number(p.get("BYMONTHDAY")) };
  }
  return { mode: "custom", rrule };
}

export function ruleToRRule(r: Rule): string {
  const interval = (n: number) => (n > 1 ? `;INTERVAL=${n}` : "");
  switch (r.mode) {
    case "weekly":
      return `FREQ=WEEKLY${interval(r.every)};BYDAY=${WEEKDAYS.filter((d) => r.days.includes(d)).join(",")}`;
    case "monthlyDay":
      return `FREQ=MONTHLY${interval(r.every)};BYMONTHDAY=${r.day}`;
    case "monthlyWeekday":
      return `FREQ=MONTHLY${interval(r.every)};BYDAY=${r.ordinal}${r.day}`;
    case "yearly":
      return `FREQ=YEARLY;BYMONTH=${r.month};BYMONTHDAY=${r.day}`;
    case "custom":
      return r.rrule;
  }
}

function everyN(n: number, one: string, many: string): string {
  return n === 1 ? `every ${one}` : n === 2 ? `every other ${one}` : `every ${n} ${many}`;
}

function ordinalDay(n: number): string {
  const s = n % 100 >= 11 && n % 100 <= 13 ? "th" : ["th", "st", "nd", "rd"][n % 10] ?? "th";
  return `${n}${s}`;
}

function listDays(days: Weekday[]): string {
  const sorted = WEEKDAYS.filter((d) => days.includes(d));
  return sorted.length === 1 ? DAY_NAMES[sorted[0]] : sorted.map((d) => SHORT_DAY[d]).join(", ");
}

export function describeRule(rrule: string): string {
  const r = parseRule(rrule);
  switch (r.mode) {
    case "weekly":
      return r.every === 1 ? `every ${listDays(r.days)}` : `${everyN(r.every, "week", "weeks")} on ${listDays(r.days)}`;
    case "monthlyDay":
      return `${everyN(r.every, "month", "months")} on the ${ordinalDay(r.day)}`;
    case "monthlyWeekday":
      return `${everyN(r.every, "month", "months")} on the ${ORDINALS[r.ordinal]} ${DAY_NAMES[r.day]}`;
    case "yearly":
      return `every year on ${MONTHS[r.month - 1]} ${r.day}`;
    case "custom":
      return r.rrule;
  }
}

function capitalize(s: string): string {
  return s.charAt(0).toUpperCase() + s.slice(1);
}

export function describeSchedule(s: Schedule | undefined): string {
  if (!s) return "";
  switch (s.kind) {
    case ScheduleKind.INTERVAL: {
      const [one, many] = UNIT_NAMES[s.intervalUnit] ?? ["", ""];
      return `Every ${s.intervalN === 1 ? one : `${s.intervalN} ${many}`} after it's done`;
    }
    case ScheduleKind.FIXED:
      return capitalize(describeRule(s.rrule)) + (s.carryOver ? ", until done" : "");
    case ScheduleKind.CYCLE:
      return `Rotation, ${describeRule(s.rrule)}`;
    case ScheduleKind.ONCE:
      return "One-time";
    default:
      return "";
  }
}
