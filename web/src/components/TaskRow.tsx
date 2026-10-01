import { api, ScheduleKind, UrgencyGroup, type Task } from "../api";
import { daysBetween, formatDate, relativeDue, relativePast } from "../dates";
import { href } from "../router";
import { describeSchedule } from "../schedule";
import { useSession } from "../session";
import { tagStyle } from "../colors";
import { activeChecklist, canDo, slotTitle, toast, undoAction, useRunner } from "./common";
import { isNetworkError, queueAction, queuedFor, unqueue, useConnectivity } from "../offline";
import { closeTaskNotification } from "../notifications";

export function TagChips({ ids }: { ids: string[] }) {
  const { tagsById } = useSession();
  const tags = ids.map((id) => tagsById.get(id)).filter((t) => t !== undefined);
  if (!tags.length) return null;
  return (
    <span class="tags">
      {tags.map((t) => (
        <span class="tag" key={t.id} style={tagStyle(t.color)}>
          {t.name}
        </span>
      ))}
    </span>
  );
}

function dueClass(group: UrgencyGroup | undefined, days: number): string {
  if (group === UrgencyGroup.OVERDUE || days < 0) return "due overdue";
  if (group === UrgencyGroup.TODAY || days === 0) return "due today";
  return "due";
}

export function DueLabel({ task, group }: { task: Task; group?: UrgencyGroup }) {
  const s = useSession();
  const st = task.state!;
  if (st.done) return <span class="due muted">{wontDo(task) ? "won't do" : "done"}</span>;
  if (st.paused) return <span class="due muted">paused{st.pauseUntil ? ` until ${formatDate(st.pauseUntil)}` : ""}</span>;
  if (!st.due) return <span class="due muted">no due date</span>;
  const ref = s.today();
  const days = daysBetween(ref, st.due);
  return (
    <span class={dueClass(group, days)} title={formatDate(st.due, ref)}>
      {relativeDue(st.due, ref)}
      {st.deferred && <span class="badge">deferred</span>}
    </span>
  );
}

/** A one-time task that was closed by skipping it rather than doing it. */
export function wontDo(task: Task): boolean {
  return !!task.state?.done && task.schedule?.kind === ScheduleKind.ONCE && !task.lastDone;
}

/** How many days a completion or skip counts as "recent". */
export const RECENT_DAYS = 3;

/** The task's latest completion or skip, if it was within RECENT_DAYS. */
export function recentActivity(task: Task, today: string): { kind: "done" | "skipped"; date: string } | null {
  const latest =
    task.lastSkipped > task.lastDone ? { kind: "skipped" as const, date: task.lastSkipped } : { kind: "done" as const, date: task.lastDone };
  return latest.date && daysBetween(latest.date, today) < RECENT_DAYS ? latest : null;
}

/** Whether the task was done or skipped within the last RECENT_DAYS days. */
export function doneRecently(task: Task, today: string): boolean {
  return recentActivity(task, today) !== null;
}

/** "✓ done yesterday" or "✕ skipped today" for a task dealt with recently. */
export function ActivityChip({ task }: { task: Task }) {
  const session = useSession();
  const today = session.today();
  const recent = recentActivity(task, today);
  if (!recent) return null;
  const label = recent.kind === "done" ? "done" : wontDo(task) ? "won't do" : "skipped";
  return (
    <span class={recent.kind === "done" ? "done-chip" : "skip-chip"} title={`${label} ${formatDate(recent.date, today)}`}>
      {recent.kind === "done" ? "✓" : "✕"} {label} {wontDo(task) ? "" : relativePast(recent.date, today)}
    </span>
  );
}

/** Whether the task's next due date is within its lead time (it would be in Upcoming). */
export function dueSoon(task: Task, today: string): boolean {
  const st = task.state!;
  return !!st.due && !st.done && !st.paused && daysBetween(today, st.due) <= task.effectiveLeadDays;
}

/**
 * A task in a list, with a quick Done button when it can be done in one tap.
 * In the "recently done" list (recent), the button is hidden.
 */
export function TaskRow({
  task,
  group,
  showLastDone,
  recent,
  onChanged,
}: {
  task: Task;
  group?: UrgencyGroup;
  showLastDone?: boolean;
  recent?: boolean;
  onChanged: (t: Task) => void;
}) {
  const session = useSession();
  const connectivity = useConnectivity();
  const { busy, error, setError, run } = useRunner();
  const st = task.state!;
  const slot = st.currentSlotId ? slotTitle(task, st.currentSlotId) : "";
  const pending = queuedFor(task.id, connectivity).find((a) => a.kind === "complete" || a.kind === "skip");
  const quickDone = canDo(task) && !st.done && !st.paused && !pending && !recent && activeChecklist(task).length === 0;
  const justDone = doneRecently(task, session.today());
  const oldDone = showLastDone && task.lastDone && !justDone;

  async function done(e: Event) {
    e.preventDefault();
    const res = await run(
      () => api.complete({ id: task.id, version: task.version }),
      async (err) => {
        if (!isNetworkError(err)) return;
        setError("");
        const item = await queueAction(task, session.today(), { kind: "complete" });
        toast(`Done: ${task.title} · will sync when you're back online`, { label: "Undo", run: () => unqueue(item.id!) });
      },
    );
    if (res?.task) {
      closeTaskNotification(task.id);
      const next = res.task.state?.due;
      toast(`Done: ${task.title}${next && !res.task.state?.done ? ` · next ${formatDate(next)}` : ""}`, undoAction(res.task, onChanged));
      onChanged(res.task);
    }
  }

  return (
    <li class={`task-row priority-${task.priority}`}>
      <a class="task-main" href={href.task(task.id)}>
        <span class="title">
          {task.priority === 1 && <span class="prio" title="High priority">!</span>}
          {task.title}
          {slot && <span class="slot"> · {slot}</span>}
        </span>
        <span class="meta">
          <DueLabel task={task} group={group} />
          <span class="muted">{describeSchedule(task.schedule)}</span>
          {task.archived && <span class="badge">archived</span>}
          {pending && <span class="badge">{pending.kind === "skip" ? "skipped" : "done"} · waiting to sync</span>}
          <TagChips ids={task.tagIds} />
          {justDone && <ActivityChip task={task} />}
          {oldDone && (
            <span class="muted small" title={`Last done ${formatDate(task.lastDone, session.today())}`}>
              done {relativePast(task.lastDone, session.today())}
            </span>
          )}
        </span>
        {error && <span class="error inline">{error}</span>}
      </a>
      {quickDone ? (
        <button class="done-button" onClick={done} disabled={busy} title="Mark done">
          ✓
        </button>
      ) : null}
    </li>
  );
}
