import { useEffect, useState } from "preact/hooks";
import { api, UrgencyGroup, type UpcomingItem } from "../api";
import { ErrorBanner, useAutoRefresh, useRunner } from "../components/common";
import { TagFilter, useTagFilter } from "../components/TagFilter";
import { TaskRow } from "../components/TaskRow";
import { href } from "../router";

const GROUPS: [UrgencyGroup, string][] = [
  [UrgencyGroup.OVERDUE, "Overdue"],
  [UrgencyGroup.TODAY, "Today"],
  [UrgencyGroup.SOON, "Coming up"],
];

export function Upcoming() {
  const [items, setItems] = useState<UpcomingItem[] | null>(null);
  const filter = useTagFilter("upcoming");
  const { error, setError, run } = useRunner();

  const load = () => run(async () => setItems((await api.upcoming({ tagIds: filter.selected })).items));
  useEffect(() => {
    load();
  }, [filter.selected.join()]);
  useAutoRefresh(load);

  return (
    <>
      <TagFilter filter={filter} />
      <ErrorBanner error={error} onDismiss={() => setError("")} />
      {items === null ? (
        <p class="muted">Loading…</p>
      ) : items.length === 0 ? (
        <div class="empty">
          <p>Nothing is due soon.</p>
          <p>
            <a href={href.tasks()}>See all tasks</a> or <a href={href.newTask()}>add one</a>.
          </p>
        </div>
      ) : (
        GROUPS.map(([group, label]) => {
          const inGroup = items.filter((i) => i.group === group);
          if (!inGroup.length) return null;
          return (
            <section key={group}>
              <h2 class={`group group-${group}`}>{label}</h2>
              <ul class="task-list">
                {inGroup.map((i) => (
                  <TaskRow key={i.task!.id} task={i.task!} group={group} onChanged={load} />
                ))}
              </ul>
            </section>
          );
        })
      )}
    </>
  );
}
