import { useEffect, useState } from "preact/hooks";
import { AccessLevel, api, EventKind, ListEventsResponseSchema, ScheduleKind, type Event, type Task } from "../api";
import { cached } from "../offline";
import { formatDate, formatTimestamp } from "../dates";
import { useSession } from "../session";
import { ErrorBanner, Markdown, slotTitle, useRunner } from "./common";

function itemTitle(task: Task, id: string): string {
  return task.checklist.find((i) => i.id === id)?.title ?? "?";
}

function describe(task: Task, e: Event, ref: string): string {
  const slot = e.slotId ? slotTitle(task, e.slotId) : "";
  const withSlot = (s: string) => (slot ? `${s}: ${slot}` : s);
  switch (e.kind) {
    case EventKind.DONE: {
      const total = e.checkedItemIds.length + e.uncheckedItemIds.length;
      const partial = e.uncheckedItemIds.length ? ` (${e.checkedItemIds.length} of ${total}; not ${e.uncheckedItemIds.map((i) => itemTitle(task, i)).join(", ")})` : "";
      return withSlot("Done") + partial;
    }
    case EventKind.MISSED:
      return e.checkedItemIds.length ? `Missed (partly done: ${e.checkedItemIds.map((i) => itemTitle(task, i)).join(", ")})` : "Missed";
    case EventKind.SKIPPED:
      if (e.merged) return "Skipped (merged into a deferral)";
      return task.schedule?.kind === ScheduleKind.ONCE ? "Won't do" : withSlot("Skipped");
    case EventKind.DEFERRED:
      return `Deferred from ${formatDate(e.from, ref)} to ${formatDate(e.to, ref)}`;
    case EventKind.DEFERRAL_CLEARED:
      return `Deferral undone, due ${formatDate(e.to, ref)}`;
    case EventKind.PAUSED:
      return e.to ? `Paused until ${formatDate(e.to, ref)}` : "Paused";
    case EventKind.RESUMED:
      return "Resumed";
    case EventKind.SLOT_SET:
      return `Next in rotation set to ${slot}`;
    case EventKind.SCHEDULE_CHANGED:
      return e.to ? `Schedule changed; now due ${formatDate(e.to, ref)}` : "Schedule changed";
    case EventKind.NOTE:
      return e.itemId ? `Note on ${itemTitle(task, e.itemId)}` : "Note";
    default:
      return "";
  }
}

const PAGE = 30;

/** A gentle green check for done, red cross for skipped or missed. */
function EventIcon({ kind, merged }: { kind: EventKind; merged: boolean }) {
  if (kind === EventKind.DONE) return <span class="event-icon done" aria-hidden="true">✓</span>;
  if (kind === EventKind.MISSED || (kind === EventKind.SKIPPED && !merged)) {
    return <span class="event-icon skipped" aria-hidden="true">✕</span>;
  }
  return <span class="event-icon" aria-hidden="true" />;
}

export function History({ task, onTaskChanged }: { task: Task; onTaskChanged: (t: Task) => void }) {
  const [events, setEvents] = useState<Event[]>([]);
  const [next, setNext] = useState("");
  const [loaded, setLoaded] = useState(false);
  const [slotFilter, setSlotFilter] = useState("");
  const { busy, error, setError, run } = useRunner();

  async function load(token = "") {
    const fetch = () => api.listEvents({ taskId: task.id, slotId: slotFilter, limit: PAGE, pageToken: token });
    const res = await run(() => (token ? fetch() : cached(`events:${task.id}:${slotFilter}`, ListEventsResponseSchema, fetch)));
    if (!res) return;
    setEvents((prev) => (token ? [...prev, ...res.events] : res.events));
    setNext(res.nextPageToken);
    setLoaded(true);
  }
  useEffect(() => {
    load();
  }, [task.id, slotFilter]);

  function replace(e: Event) {
    setEvents((list) => list.map((x) => (x.id === e.id ? e : x)));
  }

  const slots = task.slots;
  return (
    <section class="history">
      <h2>History</h2>
      {slots.length > 1 && (
        <select value={slotFilter} onChange={(e) => setSlotFilter(e.currentTarget.value)} aria-label="Filter history by slot">
          <option value="">All slots</option>
          {slots.map((s) => (
            <option key={s.id} value={s.id}>
              {s.title}
            </option>
          ))}
        </select>
      )}
      <ErrorBanner error={error} onDismiss={() => setError("")} />
      {loaded && events.length === 0 && <p class="muted">Nothing yet.</p>}
      <ol class="timeline">
        {events.map((e) => (
          <EventItem
            key={e.id}
            task={task}
            event={e}
            onChanged={(ev, t) => {
              replace(ev);
              if (t) onTaskChanged(t);
            }}
            onDeleted={(t) => {
              setEvents((list) => list.filter((x) => x.id !== e.id));
              if (t) onTaskChanged(t);
            }}
          />
        ))}
      </ol>
      {next && (
        <button class="link" disabled={busy} onClick={() => load(next)}>
          Show older
        </button>
      )}
    </section>
  );
}

function EventItem({
  task,
  event: e,
  onChanged,
  onDeleted,
}: {
  task: Task;
  event: Event;
  onChanged: (e: Event, t?: Task) => void;
  onDeleted: (t?: Task) => void;
}) {
  const session = useSession();
  const [editing, setEditing] = useState(false);
  const [date, setDate] = useState(e.date);
  const [note, setNote] = useState(e.note);
  const { busy, error, setError, run } = useRunner();
  const ref = session.today();

  const mine = !e.user || e.user.id === session.me.id;
  const canChange = task.myAccess === AccessLevel.FULL || (task.myAccess === AccessLevel.DO && mine);

  async function save(markDone = false) {
    const res = await run(() =>
      api.editEvent({
        id: e.id,
        date: date !== e.date ? date : undefined,
        note: note !== e.note ? note : undefined,
        markDone,
      }),
    );
    if (res?.event) {
      onChanged(res.event, res.task);
      setEditing(false);
    }
  }
  async function remove() {
    const res = await run(() => api.deleteEvent({ id: e.id }));
    if (res) onDeleted(res.task);
  }

  return (
    <li class={`event event-${EventKind[e.kind]?.toLowerCase()}`}>
      <div class="event-head">
        <span class="event-date">{formatDate(e.date, ref)}</span>
        <EventIcon kind={e.kind} merged={e.merged} />
        <span class="event-what">{describe(task, e, ref)}</span>
        <span class="muted small">
          {e.user ? e.user.name || e.user.email : "automatic"}
          {e.editedAt && " · edited"}
        </span>
        {canChange && !editing && (
          <span class="event-tools">
            {e.kind === EventKind.MISSED && (
              <button class="link small" disabled={busy} onClick={() => save(true)} title="We did it after all">
                mark done
              </button>
            )}
            <button class="link small" onClick={() => setEditing(true)}>
              edit
            </button>
          </span>
        )}
      </div>
      {!editing && <Markdown text={e.note} class="event-body" />}
      {editing && (
        <form
          class="inline-form"
          onSubmit={(ev) => {
            ev.preventDefault();
            save();
          }}
        >
          <label>
            Date
            <input type="date" required max={ref} value={date} onInput={(ev) => setDate(ev.currentTarget.value)} />
          </label>
          <textarea value={note} placeholder="Note" rows={2} onInput={(ev) => setNote(ev.currentTarget.value)} />
          <p class="muted small">Recorded {formatTimestamp(e.createdAt)}</p>
          <div class="button-row">
            <button class="primary" type="submit" disabled={busy}>
              Save
            </button>
            <button type="button" class="link" onClick={() => setEditing(false)}>
              Cancel
            </button>
            <button type="button" class="danger link" disabled={busy} onClick={remove}>
              Delete entry
            </button>
          </div>
        </form>
      )}
      <ErrorBanner error={error} onDismiss={() => setError("")} />
    </li>
  );
}
