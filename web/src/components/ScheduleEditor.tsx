import { IntervalUnit, ScheduleKind } from "../api";
import { describeRule, MONTHS, ORDINALS, parseRule, ruleToRRule, UNIT_NAMES, WEEKDAYS, type Rule, type Weekday } from "../schedule";

export interface ScheduleDraft {
  kind: ScheduleKind;
  intervalN: number;
  intervalUnit: IntervalUnit;
  rule: Rule;
  rruleStart: string;
  /** Fixed tasks: keep a missed date until it's done. */
  carryOver: boolean;
  /** Interval and once tasks: the (first) due date, when it can be set. */
  firstDue: string;
}

/**
 * Whether the due date of an interval or once task can be set here: always
 * for a new task, and for an existing one until it has history.
 */
export type DueMode = "new" | "editable" | "locked";

const KINDS: [ScheduleKind, string, string][] = [
  [ScheduleKind.INTERVAL, "Some time after it's done", "Like cleaning the dryer vent: doing it late pushes the next one back."],
  [ScheduleKind.FIXED, "On set dates", "Tied to the calendar, like trash day or monthly meds."],
  [ScheduleKind.CYCLE, "Rotation", "Different jobs take turns on set dates, like weekend chores."],
  [ScheduleKind.ONCE, "Just once", "A one-off job, with or without a due date."],
];

const DAY_LETTERS: Record<Weekday, string> = { MO: "M", TU: "T", WE: "W", TH: "T", FR: "F", SA: "S", SU: "S" };
const DAY_LABELS: Record<Weekday, string> = { MO: "Monday", TU: "Tuesday", WE: "Wednesday", TH: "Thursday", FR: "Friday", SA: "Saturday", SU: "Sunday" };

function NumberInput({ value, onChange, min = 1, max = 999 }: { value: number; onChange: (n: number) => void; min?: number; max?: number }) {
  return (
    <input
      class="narrow"
      type="number"
      min={min}
      max={max}
      required
      value={value}
      onInput={(e) => onChange(Math.max(min, Number(e.currentTarget.value) || min))}
    />
  );
}

function defaultRule(mode: Rule["mode"], current: Rule): Rule {
  const every = "every" in current ? current.every : 1;
  switch (mode) {
    case "weekly":
      return { mode, every, days: ["SA"] };
    case "monthlyDay":
      return { mode, every, day: 1 };
    case "monthlyWeekday":
      return { mode, every, ordinal: 1, day: "SA" };
    case "yearly":
      return { mode, month: 1, day: 1 };
    case "custom":
      return { mode, rrule: ruleToRRule(current) };
  }
}

function RuleEditor({ rule, onChange }: { rule: Rule; onChange: (r: Rule) => void }) {
  const every = (unitOne: string, unitMany: string) =>
    "every" in rule && (
      <>
        every <NumberInput value={rule.every} onChange={(n) => onChange({ ...rule, every: n } as Rule)} /> {rule.every === 1 ? unitOne : unitMany}
      </>
    );
  return (
    <div class="rule-editor">
      <select value={rule.mode} onChange={(e) => onChange(defaultRule(e.currentTarget.value as Rule["mode"], rule))} aria-label="Repeat">
        <option value="weekly">Weekly</option>
        <option value="monthlyDay">Monthly, on a date</option>
        <option value="monthlyWeekday">Monthly, on a weekday</option>
        <option value="yearly">Yearly</option>
        <option value="custom">Custom (RRULE)</option>
      </select>
      <div class="rule-fields">
        {rule.mode === "weekly" && (
          <>
            {every("week", "weeks")} on
            <span class="day-picker">
              {WEEKDAYS.map((d) => (
                <button
                  type="button"
                  key={d}
                  class={rule.days.includes(d) ? "selected" : ""}
                  aria-pressed={rule.days.includes(d)}
                  aria-label={DAY_LABELS[d]}
                  title={DAY_LABELS[d]}
                  onClick={() => onChange({ ...rule, days: rule.days.includes(d) ? rule.days.filter((x) => x !== d) : [...rule.days, d] })}
                >
                  {DAY_LETTERS[d]}
                </button>
              ))}
            </span>
          </>
        )}
        {rule.mode === "monthlyDay" && (
          <>
            {every("month", "months")} on day <NumberInput value={rule.day} max={31} onChange={(day) => onChange({ ...rule, day })} />
          </>
        )}
        {rule.mode === "monthlyWeekday" && (
          <>
            {every("month", "months")} on the{" "}
            <select value={rule.ordinal} onChange={(e) => onChange({ ...rule, ordinal: Number(e.currentTarget.value) })}>
              {[1, 2, 3, 4, -1].map((o) => (
                <option key={o} value={o}>
                  {ORDINALS[o]}
                </option>
              ))}
            </select>{" "}
            <select value={rule.day} onChange={(e) => onChange({ ...rule, day: e.currentTarget.value as Weekday })}>
              {WEEKDAYS.map((d) => (
                <option key={d} value={d}>
                  {DAY_LABELS[d]}
                </option>
              ))}
            </select>
          </>
        )}
        {rule.mode === "yearly" && (
          <>
            every year on{" "}
            <select value={rule.month} onChange={(e) => onChange({ ...rule, month: Number(e.currentTarget.value) })}>
              {MONTHS.map((m, i) => (
                <option key={m} value={i + 1}>
                  {m}
                </option>
              ))}
            </select>{" "}
            <NumberInput value={rule.day} max={31} onChange={(day) => onChange({ ...rule, day })} />
          </>
        )}
        {rule.mode === "custom" && (
          <input
            type="text"
            value={rule.rrule}
            placeholder="FREQ=WEEKLY;BYDAY=TU"
            onInput={(e) => onChange({ mode: "custom", rrule: e.currentTarget.value })}
            aria-label="RRULE"
          />
        )}
      </div>
      {rule.mode !== "custom" && <p class="muted small">Repeats {describeRule(ruleToRRule(rule))}.</p>}
    </div>
  );
}

export function ScheduleEditor({ draft, onChange, dueMode }: { draft: ScheduleDraft; onChange: (patch: Partial<ScheduleDraft>) => void; dueMode: DueMode }) {
  const calendar = draft.kind === ScheduleKind.FIXED || draft.kind === ScheduleKind.CYCLE;
  const picksPeriods = calendar && "every" in draft.rule && draft.rule.every > 1;
  const dueLabel = dueMode === "new" ? "First due" : "Next due";
  return (
    <fieldset>
      <legend>When</legend>
      <div class="kind-picker">
        {KINDS.map(([kind, label, help]) => (
          <label key={kind} class={`kind ${draft.kind === kind ? "selected" : ""}`}>
            <input type="radio" name="kind" checked={draft.kind === kind} onChange={() => onChange({ kind })} />
            <strong>{label}</strong>
            <span class="muted small">{help}</span>
          </label>
        ))}
      </div>

      {draft.kind === ScheduleKind.INTERVAL && (
        <div class="rule-fields">
          Every <NumberInput value={draft.intervalN} onChange={(intervalN) => onChange({ intervalN })} />
          <select value={draft.intervalUnit} onChange={(e) => onChange({ intervalUnit: Number(e.currentTarget.value) })} aria-label="Unit">
            {[IntervalUnit.DAYS, IntervalUnit.WEEKS, IntervalUnit.MONTHS, IntervalUnit.YEARS].map((u) => (
              <option key={u} value={u}>
                {UNIT_NAMES[u][draft.intervalN === 1 ? 0 : 1]}
              </option>
            ))}
          </select>
          after it's done
        </div>
      )}

      {calendar && <RuleEditor rule={draft.rule} onChange={(rule) => onChange({ rule })} />}
      {draft.kind === ScheduleKind.FIXED && (
        <div class="miss-policy" role="radiogroup" aria-label="If it's missed">
          <strong>If it's missed</strong>
          <label class="check">
            <input type="radio" name="carryOver" checked={!draft.carryOver} onChange={() => onChange({ carryOver: false })} />
            <span>
              Skip to the next date <span class="muted small">(like trash day)</span>
            </span>
          </label>
          <label class="check">
            <input type="radio" name="carryOver" checked={draft.carryOver} onChange={() => onChange({ carryOver: true })} />
            <span>
              Keep it until it's done <span class="muted small">(it stays overdue; the next one is still on schedule)</span>
            </span>
          </label>
        </div>
      )}
      {calendar && (
        <label>
          Starting on
          <input type="date" required value={draft.rruleStart} onInput={(e) => onChange({ rruleStart: e.currentTarget.value })} />
          <span class="muted small">
            The first date is on or after this{picksPeriods ? `; it also picks which ${draft.rule.mode === "weekly" ? "weeks" : "months"} are "on"` : ""}.
          </span>
        </label>
      )}

      {dueMode !== "locked" && draft.kind === ScheduleKind.INTERVAL && (
        <label>
          {dueLabel}
          <input type="date" required value={draft.firstDue} onInput={(e) => onChange({ firstDue: e.currentTarget.value })} />
        </label>
      )}
      {dueMode !== "locked" && draft.kind === ScheduleKind.ONCE && (
        <label>
          Due{dueMode === "new" ? " (optional)" : ""}
          <input type="date" required={dueMode !== "new"} value={draft.firstDue} onInput={(e) => onChange({ firstDue: e.currentTarget.value })} />
        </label>
      )}
      {dueMode === "locked" && (draft.kind === ScheduleKind.INTERVAL || draft.kind === ScheduleKind.ONCE) && (
        <p class="muted small">To move the due date now, use Defer on the task page.</p>
      )}
    </fieldset>
  );
}

ScheduleEditor.parse = parseRule;
