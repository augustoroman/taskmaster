import { useEffect, useState } from "preact/hooks";
import { api, ListTasksResponseSchema, type Task } from "../api";
import { cached, useOnSynced } from "../offline";
import { ErrorBanner, useAutoRefresh, useRunner } from "../components/common";
import { TagFilter, useTagFilter } from "../components/TagFilter";
import { doneRecently, dueSoon, RECENT_DAYS, recentActivity, TaskRow } from "../components/TaskRow";
import { useSession } from "../session";

export function AllTasks() {
  const [tasks, setTasks] = useState<Task[] | null>(null);
  const [showArchived, setShowArchived] = useState(false);
  const [query, setQuery] = useState("");
  const filter = useTagFilter("tasks");
  const { error, setError, run } = useRunner();

  const load = () =>
    run(async () => {
      const res = await cached(
        `tasks:${filter.selected.join()}:${showArchived}`,
        ListTasksResponseSchema,
        () => api.listTasks({ tagIds: filter.selected, includeArchived: showArchived, includeDone: showArchived }),
        (r) => r.tasks,
      );
      setTasks(res.tasks);
    });
  useEffect(() => {
    load();
  }, [filter.selected.join(), showArchived]);
  useAutoRefresh(load);
  useOnSynced(load);

  const session = useSession();
  const q = query.trim().toLowerCase();
  const shown = (tasks ?? []).filter((t) => !q || t.title.toLowerCase().includes(q));
  // Sections: due soon (as in Upcoming), then done in the last few days,
  // then everything else. The list is already sorted by due date.
  const today = session.today();
  const soon = shown.filter((t) => dueSoon(t, today));
  const recent = shown
    .filter((t) => !dueSoon(t, today) && doneRecently(t, today))
    .sort((a, b) => (b.lastDone > b.lastSkipped ? b.lastDone : b.lastSkipped).localeCompare(a.lastDone > a.lastSkipped ? a.lastDone : a.lastSkipped));
  const later = shown
    .filter((t) => !dueSoon(t, today) && !doneRecently(t, today))
    .sort((a, b) => Number(!!a.state?.paused) - Number(!!b.state?.paused));
  const sections: [string, typeof shown, boolean][] = [
    ["Coming up", soon, false],
    [recent.some((t) => recentActivity(t, today)?.kind === "skipped") ? "Recently done or skipped" : "Recently done", recent, true],
    ["Later", later, false],
  ];

  return (
    <>
      <div class="toolbar">
        <input type="search" placeholder="Search tasks" value={query} onInput={(e) => setQuery(e.currentTarget.value)} />
        <label class="check">
          <input type="checkbox" checked={showArchived} onChange={(e) => setShowArchived(e.currentTarget.checked)} />
          Archived &amp; done
        </label>
      </div>
      <TagFilter filter={filter} />
      <ErrorBanner error={error} onDismiss={() => setError("")} />
      {tasks === null ? (
        <p class="muted">Loading…</p>
      ) : shown.length === 0 ? (
        <p class="empty">{q ? "No matching tasks." : "No tasks yet."}</p>
      ) : (
        sections.map(
          ([title, list, isRecent]) =>
            list.length > 0 && (
              <section key={title} class={isRecent ? "recent" : ""}>
                <h2 class="group" title={isRecent ? `Done or skipped in the last ${RECENT_DAYS} days, and not due again soon` : undefined}>
                  {title}
                </h2>
                <ul class="task-list">
                  {list.map((t) => (
                    <TaskRow key={t.id} task={t} recent={isRecent} showLastDone onChanged={load} />
                  ))}
                </ul>
              </section>
            ),
        )
      )}
    </>
  );
}
