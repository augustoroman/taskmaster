import { useEffect, useState } from "preact/hooks";
import { AccessLevel, api, type Share, type Tag } from "../api";
import { ErrorBanner, toast, useRunner } from "../components/common";
import { useSession } from "../session";
import { PALETTE_ROWS, tagStyle } from "../colors";

const LEVELS: [AccessLevel, string, string][] = [
  [AccessLevel.READ, "Can view", "See tasks and their history."],
  [AccessLevel.DO, "Can do", "Also mark done, defer, check items and add notes."],
  [AccessLevel.FULL, "Full control", "Also edit, delete, tag, and share with others."],
];

const levelName = (l: AccessLevel) => LEVELS.find(([x]) => x === l)?.[1] ?? "";

export function Tags() {
  const session = useSession();
  const [name, setName] = useState("");
  const { busy, error, setError, run } = useRunner();
  const mine = session.tags.filter((t) => t.owner?.id === session.me.id);
  const shared = session.tags.filter((t) => t.owner?.id !== session.me.id);

  async function create(e: Event) {
    e.preventDefault();
    const res = await run(() => api.createTag({ name }));
    if (res) {
      setName("");
      await session.reloadTags();
    }
  }

  return (
    <div class="tags-page">
      <h1>Tags</h1>
      <p class="muted">
        Tags organize tasks and decide who sees them. Share a tag with someone and they see every task that has it.
      </p>
      <form class="inline" onSubmit={create}>
        <input type="text" required maxLength={100} placeholder="New tag, e.g. House" value={name} onInput={(e) => setName(e.currentTarget.value)} />
        <button class="primary" type="submit" disabled={busy}>
          Add tag
        </button>
      </form>
      <ErrorBanner error={error} onDismiss={() => setError("")} />

      {mine.length > 0 && <h2>Your tags</h2>}
      {mine.map((t) => (
        <TagCard key={t.id} tag={t} />
      ))}
      {shared.length > 0 && <h2>Shared with you</h2>}
      {shared.map((t) => (
        <TagCard key={t.id} tag={t} />
      ))}
    </div>
  );
}

function TagCard({ tag }: { tag: Tag }) {
  const session = useSession();
  const owner = tag.owner?.id === session.me.id;
  const full = tag.myAccess === AccessLevel.FULL;
  const [open, setOpen] = useState(false);
  const [renaming, setRenaming] = useState(false);
  const [name, setName] = useState(tag.name);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [coloring, setColoring] = useState(false);
  const { busy, error, setError, run } = useRunner();

  async function then(p: Promise<unknown>, msg?: string) {
    const ok = await run(() => p);
    if (ok !== undefined) {
      await session.reloadTags();
      if (msg) toast(msg);
    }
  }

  return (
    <section class="card">
      <div class="card-head">
        {renaming ? (
          <form
            class="inline"
            onSubmit={(e) => {
              e.preventDefault();
              then(api.updateTag({ id: tag.id, name, color: tag.color })).then(() => setRenaming(false));
            }}
          >
            <input type="text" required value={name} onInput={(e) => setName(e.currentTarget.value)} />
            <button class="primary" type="submit" disabled={busy}>
              Save
            </button>
            <button type="button" class="link" onClick={() => setRenaming(false)}>
              Cancel
            </button>
          </form>
        ) : (
          <h3>
            <span class="tag tag-lg" style={tagStyle(tag.color)}>
              {tag.name}
            </span>
            {!owner && <span class="muted small"> · {tag.owner?.name || tag.owner?.email} · {levelName(tag.myAccess)}</span>}
          </h3>
        )}
        <label class="check small" title="Hide this tag's tasks from your lists. Doesn't change anyone's access.">
          <input type="checkbox" checked={tag.hidden} onChange={(e) => then(api.setTagHidden({ id: tag.id, hidden: e.currentTarget.checked }))} />
          Hide
        </label>
      </div>
      <div class="button-row">
        {full && (
          <button class="link" onClick={() => setOpen(!open)}>
            {open ? "Hide sharing" : "Sharing…"}
          </button>
        )}
        <button class="link" onClick={() => setColoring(!coloring)}>
          Color…
        </button>
        {owner && !renaming && (
          <button class="link" onClick={() => setRenaming(true)}>
            Rename
          </button>
        )}
        {owner && (
          <button class="link danger" onClick={() => setConfirmDelete(!confirmDelete)}>
            Delete…
          </button>
        )}
      </div>
      {coloring && (
        <div class="color-picker">
          {PALETTE_ROWS.map((row, i) => (
            <div class="swatch-row" key={i}>
              {row.map((c) => (
                <button
                  key={c}
                  class={`swatch ${c === tag.color ? "selected" : ""}`}
                  style={{ background: c }}
                  aria-label={`Color ${c}`}
                  aria-pressed={c === tag.color}
                  disabled={busy}
                  onClick={() => then(api.setTagColor({ id: tag.id, color: c }))}
                />
              ))}
            </div>
          ))}
          <label class="check small custom-color">
            <input
              type="color"
              value={tag.color || "#cccccc"}
              onChange={(e) => then(api.setTagColor({ id: tag.id, color: e.currentTarget.value }))}
            />
            Custom
          </label>
          {!owner && <span class="muted small">Only changes your color, not anyone else's.</span>}
        </div>
      )}
      {confirmDelete && (
        <div class="inline-form">
          <p>
            Delete the tag <strong>{tag.name}</strong>? Its tasks stay, but people who could see them only through this tag
            will lose access.
          </p>
          <div class="button-row">
            <button class="danger" disabled={busy} onClick={() => then(api.deleteTag({ id: tag.id }), `Deleted ${tag.name}`)}>
              Delete tag
            </button>
            <button class="link" onClick={() => setConfirmDelete(false)}>
              Cancel
            </button>
          </div>
        </div>
      )}
      <ErrorBanner error={error} onDismiss={() => setError("")} />
      {open && full && <Sharing tag={tag} />}
    </section>
  );
}

function Sharing({ tag }: { tag: Tag }) {
  const session = useSession();
  const [shares, setShares] = useState<Share[]>([]);
  const [email, setEmail] = useState("");
  const [level, setLevel] = useState(AccessLevel.DO);
  const { busy, error, setError, run } = useRunner();

  const load = () => run(async () => setShares((await api.listShares({ tagId: tag.id })).shares));
  useEffect(() => {
    load();
  }, [tag.id]);

  async function add(e: Event) {
    e.preventDefault();
    const res = await run(() => api.shareTag({ tagId: tag.id, email, level }));
    if (res) {
      setEmail("");
      toast(res.share?.user ? `Shared with ${email}` : `Invited ${email}. They can log in with that Google account.`);
      load();
    }
  }

  return (
    <div class="sharing">
      <ul class="shares">
        <li>
          <span>{tag.owner?.name || tag.owner?.email}</span>
          <span class="muted small">owner</span>
        </li>
        {shares.map((s) => (
          <li key={s.id}>
            <span>
              {s.user?.name || s.email}
              {s.user?.name && <span class="muted small"> {s.email}</span>}
              {!s.user && <span class="badge">invited</span>}
            </span>
            <select
              value={s.level}
              disabled={busy}
              onChange={(e) => run(() => api.updateShare({ id: s.id, level: Number(e.currentTarget.value) })).then(load)}
              aria-label={`Access for ${s.email}`}
            >
              {LEVELS.map(([l, label]) => (
                <option key={l} value={l}>
                  {label}
                </option>
              ))}
            </select>
            <button
              class="link danger small"
              disabled={busy}
              onClick={() =>
                run(() => api.revokeShare({ id: s.id })).then(async () => {
                  if (s.user?.id === session.me.id) await session.reloadTags();
                  else load();
                })
              }
            >
              {s.user?.id === session.me.id ? "Leave" : "Remove"}
            </button>
          </li>
        ))}
      </ul>
      <form class="inline" onSubmit={add}>
        <input type="email" required placeholder="Email (Google account)" value={email} onInput={(e) => setEmail(e.currentTarget.value)} />
        <select value={level} onChange={(e) => setLevel(Number(e.currentTarget.value))} aria-label="Access">
          {LEVELS.map(([l, label]) => (
            <option key={l} value={l}>
              {label}
            </option>
          ))}
        </select>
        <button class="primary" type="submit" disabled={busy}>
          Share
        </button>
      </form>
      <p class="muted small">{LEVELS.find(([l]) => l === level)?.[2]}</p>
      <ErrorBanner error={error} onDismiss={() => setError("")} />
    </div>
  );
}
