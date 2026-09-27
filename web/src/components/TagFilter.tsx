import { useState } from "preact/hooks";
import { useSession } from "../session";
import { tagStyle } from "../colors";

function load(key: string): string[] {
  try {
    return JSON.parse(localStorage.getItem(`tagFilter:${key}`) ?? "[]");
  } catch {
    return [];
  }
}

/** Selected tags for a list, remembered per page in this browser. */
export function useTagFilter(key: string) {
  const { tagsById } = useSession();
  const [selected, setSelected] = useState<string[]>(() => load(key));
  const valid = selected.filter((id) => tagsById.has(id));
  return {
    selected: valid,
    toggle(id: string) {
      const next = valid.includes(id) ? valid.filter((x) => x !== id) : [...valid, id];
      setSelected(next);
      try {
        localStorage.setItem(`tagFilter:${key}`, JSON.stringify(next));
      } catch {
        // Not remembered; that's fine.
      }
    },
  };
}

export function TagFilter({ filter }: { filter: ReturnType<typeof useTagFilter> }) {
  const { tags } = useSession();
  const visible = tags.filter((t) => !t.hidden || filter.selected.includes(t.id));
  if (visible.length < 2) return null;
  return (
    <div class="tag-filter" role="group" aria-label="Filter by tag">
      {visible.map((t) => (
        <button
          key={t.id}
          class={`tag ${filter.selected.includes(t.id) ? "selected" : ""}`}
          style={tagStyle(t.color)}
          aria-pressed={filter.selected.includes(t.id)}
          onClick={() => filter.toggle(t.id)}
        >
          {t.name}
        </button>
      ))}
    </div>
  );
}
