interface Item {
  id: string;
  title: string;
}

let nextKey = 1;

/** A placeholder ID for an item that hasn't been saved yet. */
export const newItemID = () => `new:${nextKey++}`;

/** The ID to send to the server: "" for unsaved items. */
export const savedID = (id: string) => (id.startsWith("new:") ? "" : id);

/** Edits an ordered list of titled items (checklist items, rotation steps). */
export function ListEditor<T extends Item>({
  items,
  onChange,
  make,
  placeholder,
  addLabel,
  numbered,
}: {
  items: T[];
  onChange: (items: T[]) => void;
  make: () => T;
  placeholder: string;
  addLabel: string;
  numbered?: boolean;
}) {
  const update = (i: number, patch: Partial<T>) => onChange(items.map((x, j) => (j === i ? { ...x, ...patch } : x)));
  const move = (i: number, by: number) => {
    const next = [...items];
    [next[i], next[i + by]] = [next[i + by], next[i]];
    onChange(next);
  };
  return (
    <div class="list-editor">
      <ol class={numbered ? "" : "plain"}>
        {items.map((item, i) => (
          <li key={item.id}>
            <input type="text" value={item.title} placeholder={placeholder} onInput={(e) => update(i, { title: e.currentTarget.value } as Partial<T>)} />
            <button type="button" class="icon" disabled={i === 0} onClick={() => move(i, -1)} aria-label="Move up">
              ↑
            </button>
            <button type="button" class="icon" disabled={i === items.length - 1} onClick={() => move(i, 1)} aria-label="Move down">
              ↓
            </button>
            <button type="button" class="icon" onClick={() => onChange(items.filter((_, j) => j !== i))} aria-label="Remove">
              ✕
            </button>
          </li>
        ))}
      </ol>
      <button type="button" class="link" onClick={() => onChange([...items, make()])}>
        + {addLabel}
      </button>
    </div>
  );
}
