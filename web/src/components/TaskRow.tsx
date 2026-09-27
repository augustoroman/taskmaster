import { api, UrgencyGroup, type Task } from "../api";
import { daysBetween, formatDate, relativeDue, relativePast } from "../dates";
import { href } from "../router";
import { describeSchedule } from "../schedule";
import { useSession } from "../session";
import { activeChecklist, canDo, slotTitle, toast, useRunner } from "./common";

export function TagChips({ ids }: { ids: string[] }) {
  const { tagsById } = useSession();
  const tags = ids.map((id) => tagsById.get(id)).filter((t) => t !== undefined);
  if (!tags.length) return null;
  return (
    <span class="tags">
      {tags.map((t) => (
        <span class="tag" key={t.id} style={t.color ? { borderColor: t.color } : undefined}>
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
  if (st.done) return <span class="due muted">done</span>;
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

/** A task in a list, with a quick Done button when it can be done in one tap. */
export function TaskRow({
  task,
  group,
  showLastDone,
  onChanged,
}: {
  task: Task;
  group?: UrgencyGroup;
  showLastDone?: boolean;
  onChanged: (t: Task) => void;
}) {
  const session = useSession();
  const { busy, error, run } = useRunner();
  const st = task.state!;
  const slot = st.currentSlotId ? slotTitle(task, st.currentSlotId) : "";
  const quickDone = canDo(task) && !st.done && !st.paused && activeChecklist(task).length === 0;

  async function done(e: Event) {
    e.preventDefault();
    const res = await run(() => api.complete({ id: task.id, version: task.version }));
    if (res?.task) {
      const next = res.task.state?.due;
      toast(`Done: ${task.title}${next && !res.task.state?.done ? ` · next ${formatDate(next)}` : ""}`);
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
          <TagChips ids={task.tagIds} />
          {showLastDone && task.lastDone && (
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
