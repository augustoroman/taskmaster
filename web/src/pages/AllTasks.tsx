import { useEffect, useState } from "preact/hooks";
import { api, type Task } from "../api";
import { ErrorBanner, useAutoRefresh, useRunner } from "../components/common";
import { TagFilter, useTagFilter } from "../components/TagFilter";
import { TaskRow } from "../components/TaskRow";

export function AllTasks() {
  const [tasks, setTasks] = useState<Task[] | null>(null);
  const [showArchived, setShowArchived] = useState(false);
  const [query, setQuery] = useState("");
  const filter = useTagFilter("tasks");
  const { error, setError, run } = useRunner();

  const load = () =>
    run(async () => {
      const res = await api.listTasks({ tagIds: filter.selected, includeArchived: showArchived, includeDone: showArchived });
      setTasks(res.tasks);
    });
  useEffect(() => {
    load();
  }, [filter.selected.join(), showArchived]);
  useAutoRefresh(load);

  const q = query.trim().toLowerCase();
  const shown = (tasks ?? []).filter((t) => !q || t.title.toLowerCase().includes(q));

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
        <ul class="task-list">
          {shown.map((t) => (
            <TaskRow key={t.id} task={t} onChanged={load} />
          ))}
        </ul>
      )}
    </>
  );
}
