import { useEffect, useState } from "preact/hooks";
import type { MessageInitShape } from "@bufbuild/protobuf";
import { api, IntervalUnit, isConflict, ScheduleKind, type Task, type TaskInputSchema } from "../api";
import { ErrorBanner, MarkdownField, useRunner } from "../components/common";
import { ScheduleEditor, type ScheduleDraft } from "../components/ScheduleEditor";
import { ListEditor, newItemID, savedID } from "../components/ListEditor";
import { today } from "../dates";
import { href, navigate } from "../router";
import { ruleToRRule } from "../schedule";
import { assignableTags, useSession } from "../session";

interface Draft extends ScheduleDraft {
  title: string;
  description: string;
  priority: number;
  leadDays: number;
  timeZone: string;
  slots: { id: string; title: string; description: string }[];
  checklist: { id: string; title: string }[];
  tagIds: string[];
}

function newDraft(tz: string): Draft {
  return {
    title: "",
    description: "",
    priority: 2,
    leadDays: 0,
    timeZone: tz,
    kind: ScheduleKind.INTERVAL,
    intervalN: 1,
    intervalUnit: IntervalUnit.MONTHS,
    rule: { mode: "weekly", every: 1, days: ["SA"] },
    rruleStart: today(tz),
    firstDue: today(tz),
    slots: [],
    checklist: [],
    tagIds: [],
  };
}

function draftFrom(t: Task): Draft {
  const s = t.schedule!;
  const base = newDraft(t.timeZone);
  return {
    ...base,
    title: t.title,
    description: t.description,
    priority: t.priority,
    leadDays: t.leadDays,
    timeZone: t.timeZone,
    kind: s.kind,
    intervalN: s.intervalN || base.intervalN,
    intervalUnit: s.intervalUnit || base.intervalUnit,
    rule: s.rrule ? ScheduleEditor.parse(s.rrule) : base.rule,
    rruleStart: s.rruleStart || base.rruleStart,
    firstDue: "",
    slots: t.slots.filter((x) => !x.removed).map(({ id, title, description }) => ({ id, title, description })),
    checklist: t.checklist.filter((x) => !x.removed).map(({ id, title }) => ({ id, title })),
    tagIds: t.tagIds,
  };
}

function toInput(d: Draft): MessageInitShape<typeof TaskInputSchema> {
  const calendar = d.kind === ScheduleKind.FIXED || d.kind === ScheduleKind.CYCLE;
  return {
    title: d.title,
    description: d.description,
    priority: d.priority,
    leadDays: d.leadDays,
    timeZone: d.timeZone,
    schedule: {
      kind: d.kind,
      intervalN: d.intervalN,
      intervalUnit: d.intervalUnit,
      rrule: calendar ? ruleToRRule(d.rule) : "",
      rruleStart: calendar ? d.rruleStart : "",
    },
    slots: d.kind === ScheduleKind.CYCLE ? d.slots.filter((x) => x.title.trim()).map((x) => ({ ...x, id: savedID(x.id) })) : [],
    checklist: d.kind === ScheduleKind.CYCLE ? [] : d.checklist.filter((x) => x.title.trim()).map((x) => ({ ...x, id: savedID(x.id) })),
  };
}

function problems(d: Draft): string {
  if (!d.title.trim()) return "Give the task a title.";
  if (d.kind === ScheduleKind.INTERVAL && !(d.intervalN >= 1)) return "The interval must be at least 1.";
  if ((d.kind === ScheduleKind.FIXED || d.kind === ScheduleKind.CYCLE) && d.rule.mode === "weekly" && d.rule.days.length === 0)
    return "Pick at least one day of the week.";
  if (d.kind === ScheduleKind.CYCLE && d.slots.filter((s) => s.title.trim()).length === 0) return "Add at least one step to the rotation.";
  return "";
}

export function TaskEdit({ id }: { id?: string }) {
  const session = useSession();
  const [task, setTask] = useState<Task | null>(null);
  const [draft, setDraft] = useState<Draft | null>(id ? null : newDraft(session.me.timeZone || "UTC"));
  const { busy, error, setError, run } = useRunner();

  useEffect(() => {
    if (!id) return;
    run(async () => {
      const t = (await api.getTask({ id })).task!;
      setTask(t);
      setDraft(draftFrom(t));
    });
  }, [id]);

  if (!draft) return error ? <ErrorBanner error={error} /> : <p class="muted">Loading…</p>;
  const set = (patch: Partial<Draft>) => setDraft({ ...draft, ...patch });

  async function save(e: Event) {
    e.preventDefault();
    const problem = problems(draft!);
    if (problem) {
      setError(problem);
      return;
    }
    const input = toInput(draft!);
    const res = await run(
      (): Promise<{ task?: Task }> =>
        task
          ? api.updateTask({ id: task.id, version: task.version, task: input, updateTags: true, tagIds: draft!.tagIds })
          : api.createTask({ task: input, tagIds: draft!.tagIds, firstDue: draft!.firstDue }),
      (err) => {
        if (isConflict(err)) setError("Someone else changed this task while you were editing. Reload to see their changes; your edits here will be lost.");
      },
    );
    if (res?.task) navigate(href.task(res.task.id));
  }

  // Tags you can add, plus any already on the task (which you can remove).
  const assignable = assignableTags(session);
  const tags = [
    ...assignable,
    ...(task?.tagIds ?? []).filter((id) => !assignable.some((t) => t.id === id)).flatMap((id) => session.tagsById.get(id) ?? []),
  ];
  return (
    <form class="task-edit" onSubmit={save}>
      <h1>{task ? "Edit task" : "New task"}</h1>
      <label>
        Title
        <input type="text" required maxLength={200} value={draft.title} onInput={(e) => set({ title: e.currentTarget.value })} autoFocus={!task} />
      </label>

      <ScheduleEditor draft={draft} onChange={(patch) => set(patch)} isNew={!task} />

      {draft.kind === ScheduleKind.CYCLE ? (
        <fieldset>
          <legend>Rotation</legend>
          <p class="muted small">One step is due on each scheduled date. If a date is missed, the same step carries over.</p>
          <ListEditor
            items={draft.slots}
            onChange={(slots) => set({ slots })}
            make={() => ({ id: newItemID(), title: "", description: "" })}
            placeholder="e.g. Vacuum bedrooms"
            addLabel="Add step"
            numbered
          />
        </fieldset>
      ) : (
        <fieldset>
          <legend>Checklist (optional)</legend>
          <p class="muted small">For a task with parts, like one per smoke detector. It's done when every item is checked.</p>
          <ListEditor
            items={draft.checklist}
            onChange={(checklist) => set({ checklist })}
            make={() => ({ id: newItemID(), title: "" })}
            placeholder="e.g. Hallway detector"
            addLabel="Add item"
          />
        </fieldset>
      )}

      <label>Details</label>
      <MarkdownField value={draft.description} onChange={(description) => set({ description })} placeholder="Notes, instructions, links…" />

      <label>
        Priority
        <select value={draft.priority} onChange={(e) => set({ priority: Number(e.currentTarget.value) })}>
          <option value={1}>High</option>
          <option value={2}>Normal</option>
          <option value={3}>Low</option>
        </select>
      </label>

      {tags.length > 0 && (
        <fieldset>
          <legend>Tags</legend>
          <p class="muted small">Tags decide who else can see this task. Without tags, it's private to you.</p>
          <div class="tag-picker">
            {tags.map((t) => (
              <label class="check" key={t.id}>
                <input
                  type="checkbox"
                  checked={draft.tagIds.includes(t.id)}
                  disabled={!draft.tagIds.includes(t.id) && !assignable.includes(t)}
                  onChange={(e) =>
                    set({ tagIds: e.currentTarget.checked ? [...draft.tagIds, t.id] : draft.tagIds.filter((x) => x !== t.id) })
                  }
                />
                {t.name}
              </label>
            ))}
          </div>
        </fieldset>
      )}

      <details>
        <summary>More options</summary>
        <label>
          Show in Upcoming this many days before it's due
          <input
            type="number"
            min={0}
            max={365}
            value={draft.leadDays || ""}
            placeholder={task ? `automatic (${task.effectiveLeadDays})` : "automatic"}
            onInput={(e) => set({ leadDays: Number(e.currentTarget.value) || 0 })}
          />
        </label>
        <label>
          Time zone
          <input type="text" value={draft.timeZone} onInput={(e) => set({ timeZone: e.currentTarget.value })} />
        </label>
      </details>

      <ErrorBanner error={error} onDismiss={() => setError("")} />
      <div class="button-row">
        <button class="primary" type="submit" disabled={busy}>
          {task ? "Save" : "Create task"}
        </button>
        <a class="button link" href={task ? href.task(task.id) : href.upcoming()}>
          Cancel
        </a>
      </div>
    </form>
  );
}
