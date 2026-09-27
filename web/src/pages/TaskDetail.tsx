import { useEffect, useState } from "preact/hooks";
import { api, isConflict, ScheduleKind, type ActionResponse, type Task } from "../api";
import { addDays, formatDate, relativeDue, relativePast } from "../dates";
import { href, navigate } from "../router";
import { describeSchedule } from "../schedule";
import { assignableTags, useSession } from "../session";
import { activeChecklist, activeSlots, canDo, canEdit, ErrorBanner, Markdown, slotTitle, toast, undoAction, useRunner } from "../components/common";
import { History } from "../components/History";
import { TagChips } from "../components/TaskRow";

type Form = "" | "done" | "note" | "defer" | "pause" | "delete";

export function TaskDetail({ id }: { id: string }) {
  const [task, setTask] = useState<Task | null>(null);
  const [historyKey, setHistoryKey] = useState(0);
  const { busy, error, setError, run } = useRunner();

  const load = () => run(async () => setTask((await api.getTask({ id })).task!));
  useEffect(() => {
    load();
  }, [id]);

  /** Applies an action's result; on a version conflict, reloads the task. */
  async function act(fn: () => Promise<ActionResponse | { task?: Task }>, message?: string) {
    const res = await run(fn, (err) => isConflict(err) && load());
    if (!res?.task) return false;
    setTask(res.task);
    setHistoryKey((k) => k + 1);
    if (message) {
      // Actions (not other edits) can be undone.
      const undoable = "events" in res;
      toast(
        message,
        undoable
          ? undoAction(res.task, (t) => {
              setTask(t);
              setHistoryKey((k) => k + 1);
            })
          : undefined,
      );
    }
    return true;
  }

  if (!task) return error ? <ErrorBanner error={error} /> : <p class="muted">Loading…</p>;
  return (
    <article class="task-detail">
      <Header task={task} onChanged={setTask} />
      <ErrorBanner error={error} onDismiss={() => setError("")} />
      <Status task={task} />
      {!task.archived && canDo(task) && <Actions task={task} busy={busy} act={act} />}
      {task.archived && canEdit(task) && (
        <p class="notice">
          This task is archived.{" "}
          <button class="link" disabled={busy} onClick={() => act(() => api.unarchiveTask({ id }), "Restored")}>
            Restore it
          </button>
        </p>
      )}
      <Markdown text={task.description} class="description" />
      {task.schedule?.kind === ScheduleKind.CYCLE && <Rotation task={task} busy={busy} act={act} />}
      <History key={historyKey} task={task} onTaskChanged={setTask} />
      {canEdit(task) && <Manage task={task} busy={busy} act={act} />}
    </article>
  );
}

type ActFn = (fn: () => Promise<ActionResponse | { task?: Task }>, message?: string) => Promise<boolean>;

function Header({ task, onChanged }: { task: Task; onChanged: (t: Task) => void }) {
  const session = useSession();
  const { error, setError, run } = useRunner();
  const addable = assignableTags(session).filter((t) => !task.tagIds.includes(t.id));

  async function addTag(tagId: string) {
    const res = await run(() => api.addTaskTag({ taskId: task.id, tagId }));
    if (res?.task) onChanged(res.task);
  }
  async function removeTag(tagId: string) {
    const res = await run(() => api.removeTaskTag({ taskId: task.id, tagId }));
    if (res?.task) onChanged(res.task);
  }

  return (
    <header class="detail-header">
      <h1>
        {task.priority === 1 && <span class="prio" title="High priority">!</span>}
        {task.title}
      </h1>
      <p class="muted">
        {describeSchedule(task.schedule)}
        {task.priority === 3 && " · low priority"}
      </p>
      <div class="tags">
        {canEdit(task) ? (
          <>
            {task.tagIds.map((tid) => (
              <span class="tag" key={tid}>
                {session.tagsById.get(tid)?.name ?? "?"}
                <button class="link" aria-label="Remove tag" onClick={() => removeTag(tid)}>
                  ✕
                </button>
              </span>
            ))}
            {addable.length > 0 && (
              <select
                class="add-tag"
                value=""
                onChange={(e) => {
                  const v = e.currentTarget.value;
                  e.currentTarget.value = "";
                  if (v) addTag(v);
                }}
              >
                <option value="">+ tag</option>
                {addable.map((t) => (
                  <option key={t.id} value={t.id}>
                    {t.name}
                  </option>
                ))}
              </select>
            )}
            {task.tagIds.length === 0 && <span class="muted small">Private: add a tag to share it.</span>}
          </>
        ) : (
          <TagChips ids={task.tagIds} />
        )}
      </div>
      <ErrorBanner error={error} onDismiss={() => setError("")} />
    </header>
  );
}

function Status({ task }: { task: Task }) {
  const session = useSession();
  const st = task.state!;
  const ref = session.today();
  const slot = st.currentSlotId ? slotTitle(task, st.currentSlotId) : "";
  const later = task.projected.slice(1, 4);
  return (
    <section class="status">
      {st.done ? (
        <p>
          <strong>Done.</strong> {task.schedule?.kind !== ScheduleKind.ONCE && "This schedule has ended."}
        </p>
      ) : st.paused ? (
        <p>
          <strong>Paused</strong>
          {st.pauseUntil ? ` until ${formatDate(st.pauseUntil, ref)}` : ""}.
        </p>
      ) : st.due ? (
        <p>
          {slot && (
            <>
              <strong>{slot}</strong>{" "}
            </>
          )}
          due <strong>{formatDate(st.due, ref)}</strong> <span class="muted">({relativeDue(st.due, ref)})</span>
          {st.deferred && <span class="muted"> · deferred from {formatDate(st.deferredFrom, ref)}</span>}
        </p>
      ) : (
        <p class="muted">No due date.</p>
      )}
      {task.lastDone && (
        <p class="muted small">
          Last done {formatDate(task.lastDone, ref)} ({relativePast(task.lastDone, ref)})
        </p>
      )}
      {later.length > 0 && !st.paused && (
        <p class="muted small">
          Then{" "}
          {later
            .map((p) => `${formatDate(p.due, ref)}${p.slotId ? ` (${slotTitle(task, p.slotId)})` : ""}`)
            .join(", ")}
          {task.schedule?.kind === ScheduleKind.INTERVAL && " if done on time"}
        </p>
      )}
    </section>
  );
}

function Actions({ task, busy, act }: { task: Task; busy: boolean; act: ActFn }) {
  const session = useSession();
  const [form, setForm] = useState<Form>("");
  const [note, setNote] = useState("");
  const [date, setDate] = useState("");
  const [asSlot, setAsSlot] = useState("");
  const st = task.state!;
  const items = activeChecklist(task);
  const checked = new Map(st.checks.map((c) => [c.itemId, c]));
  const kind = task.schedule?.kind;
  const v = { id: task.id, version: task.version };

  useEffect(() => {
    setNote("");
    setAsSlot("");
  }, [task.version]);

  function open(f: Form, initialDate = "") {
    setForm(form === f ? "" : f);
    setNote("");
    setDate(initialDate);
  }
  async function submit(fn: () => Promise<ActionResponse | { task?: Task }>, message: string) {
    if (await act(fn, message)) setForm("");
  }

  if (st.done) return null;
  if (st.paused) {
    return canEdit(task) ? (
      <section class="actions">
        <button class="primary" disabled={busy} onClick={() => act(() => api.resume(v), "Resumed")}>
          Resume
        </button>
      </section>
    ) : null;
  }

  const slots = activeSlots(task);
  return (
    <section class="actions">
      {items.length > 0 && (
        <ul class="checklist">
          {items.map((item) => {
            const c = checked.get(item.id);
            return (
              <li key={item.id}>
                <label class="check">
                  <input
                    type="checkbox"
                    checked={!!c}
                    disabled={busy}
                    onChange={() =>
                      act(
                        () => (c ? api.uncheckItem({ ...v, itemId: item.id }) : api.checkItem({ ...v, itemId: item.id })),
                        c ? undefined : checked.size + 1 === items.length ? `Done: ${task.title}` : undefined,
                      )
                    }
                  />
                  {item.title}
                </label>
                {c && (
                  <span class="muted small">
                    {formatDate(c.date, session.today())}
                    {c.user && c.user.id !== session.me.id ? ` · ${c.user.name || c.user.email}` : ""}
                  </span>
                )}
              </li>
            );
          })}
        </ul>
      )}

      <div class="button-row">
        {items.length === 0 ? (
          <button class="primary" disabled={busy} onClick={() => open("done")}>
            Mark done…
          </button>
        ) : (
          <button disabled={busy} onClick={() => open("done")}>
            Complete anyway…
          </button>
        )}
        <button disabled={busy} onClick={() => open("defer", addDays(st.due || session.today(), 7))}>
          Defer…
        </button>
        {st.deferred && (
          <button disabled={busy} onClick={() => act(() => api.clearDeferral(v), "Deferral cleared")}>
            Undo deferral
          </button>
        )}
        {(kind === ScheduleKind.FIXED || kind === ScheduleKind.CYCLE) && (
          <button disabled={busy} onClick={() => act(() => api.skip({ ...v, note }), "Skipped")}>
            Skip this one
          </button>
        )}
        <button disabled={busy} onClick={() => open("note")}>
          Add note…
        </button>
      </div>

      {form === "done" && (
        <form
          class="inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            submit(() => api.complete({ ...v, note, asSlotId: asSlot, force: items.length > 0 }), `Done: ${task.title}`);
          }}
        >
          {kind === ScheduleKind.CYCLE && slots.length > 1 && (
            <label>
              What was done
              <select value={asSlot || st.currentSlotId} onChange={(e) => setAsSlot(e.currentTarget.value)}>
                {slots.map((s) => (
                  <option key={s.id} value={s.id}>
                    {s.title}
                    {s.id === st.currentSlotId ? " (scheduled)" : ""}
                  </option>
                ))}
              </select>
            </label>
          )}
          <textarea placeholder="Note (optional)" value={note} onInput={(e) => setNote(e.currentTarget.value)} rows={2} />
          <p class="muted small">Did it on another day? Mark it done, then edit the date in the history below.</p>
          <div class="button-row">
            <button class="primary" type="submit" disabled={busy}>
              Mark done
            </button>
            <button type="button" class="link" onClick={() => setForm("")}>
              Cancel
            </button>
          </div>
        </form>
      )}

      {form === "defer" && (
        <form
          class="inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            submit(() => api.defer({ ...v, to: date, note }), `Deferred to ${formatDate(date)}`);
          }}
        >
          <label>
            New due date
            <input type="date" required min={session.today()} value={date} onInput={(e) => setDate(e.currentTarget.value)} />
          </label>
          {kind === ScheduleKind.FIXED && (
            <p class="muted small">Scheduled dates up to this one will be merged into this deferral.</p>
          )}
          <textarea placeholder="Why? (optional)" value={note} onInput={(e) => setNote(e.currentTarget.value)} rows={2} />
          <div class="button-row">
            <button class="primary" type="submit" disabled={busy}>
              Defer
            </button>
            <button type="button" class="link" onClick={() => setForm("")}>
              Cancel
            </button>
          </div>
        </form>
      )}

      {form === "note" && (
        <form
          class="inline-form"
          onSubmit={(e) => {
            e.preventDefault();
            submit(
              async () => {
                await api.addNote({ taskId: task.id, note });
                return { task };
              },
              "Note added",
            );
          }}
        >
          <textarea
            placeholder="e.g. Can't do this until we buy a new filter"
            required
            value={note}
            onInput={(e) => setNote(e.currentTarget.value)}
            rows={3}
          />
          <div class="button-row">
            <button class="primary" type="submit" disabled={busy}>
              Add note
            </button>
            <button type="button" class="link" onClick={() => setForm("")}>
              Cancel
            </button>
          </div>
        </form>
      )}
    </section>
  );
}

function Rotation({ task, busy, act }: { task: Task; busy: boolean; act: ActFn }) {
  const current = task.state!.currentSlotId;
  const slots = activeSlots(task);
  return (
    <section>
      <h2>Rotation</h2>
      <ol class="rotation">
        {slots.map((s) => (
          <li key={s.id} class={s.id === current ? "current" : ""}>
            <strong>{s.title}</strong>
            {s.id === current ? (
              <span class="badge">next</span>
            ) : (
              canDo(task) &&
              !task.archived && (
                <button
                  class="link small"
                  disabled={busy}
                  onClick={() => act(() => api.setCycleSlot({ id: task.id, version: task.version, slotId: s.id }), `Next up: ${s.title}`)}
                >
                  make next
                </button>
              )
            )}
            <Markdown text={s.description} class="small" />
          </li>
        ))}
      </ol>
    </section>
  );
}

function Manage({ task, busy, act }: { task: Task; busy: boolean; act: ActFn }) {
  const session = useSession();
  const [form, setForm] = useState<Form>("");
  const [until, setUntil] = useState("");
  const [note, setNote] = useState("");
  const { run, error, setError } = useRunner();
  const st = task.state!;
  const v = { id: task.id, version: task.version };

  return (
    <section class="manage">
      <div class="button-row">
        <a class="button" href={href.edit(task.id)}>
          Edit
        </a>
        {!task.archived && !st.paused && !st.done && (
          <button disabled={busy} onClick={() => setForm(form === "pause" ? "" : "pause")}>
            Pause…
          </button>
        )}
        {!task.archived && (
          <button disabled={busy} onClick={() => act(() => api.archiveTask({ id: task.id }), "Archived")}>
            Archive
          </button>
        )}
        <button class="danger" disabled={busy} onClick={() => setForm(form === "delete" ? "" : "delete")}>
          Delete…
        </button>
      </div>
      {form === "pause" && (
        <form
          class="inline-form"
          onSubmit={async (e) => {
            e.preventDefault();
            if (await act(() => api.pause({ ...v, until, note }), "Paused")) setForm("");
          }}
        >
          <p class="muted small">A paused task is hidden and nothing counts as missed.</p>
          <label>
            Resume automatically on (optional)
            <input type="date" min={addDays(session.today(), 1)} value={until} onInput={(e) => setUntil(e.currentTarget.value)} />
          </label>
          <textarea placeholder="Why? (optional)" value={note} onInput={(e) => setNote(e.currentTarget.value)} rows={2} />
          <div class="button-row">
            <button class="primary" type="submit" disabled={busy}>
              Pause
            </button>
            <button type="button" class="link" onClick={() => setForm("")}>
              Cancel
            </button>
          </div>
        </form>
      )}
      {form === "delete" && (
        <div class="inline-form">
          <p>
            Delete <strong>{task.title}</strong> and all of its history for everyone? This can't be undone.
            {!task.archived && " Archiving keeps the history instead."}
          </p>
          <div class="button-row">
            <button
              class="danger"
              onClick={async () => {
                const ok = await run(() => api.deleteTask({ id: task.id }));
                if (ok) {
                  toast(`Deleted ${task.title}`);
                  navigate(href.tasks());
                }
              }}
            >
              Delete forever
            </button>
            <button class="link" onClick={() => setForm("")}>
              Cancel
            </button>
          </div>
          <ErrorBanner error={error} onDismiss={() => setError("")} />
        </div>
      )}
    </section>
  );
}
