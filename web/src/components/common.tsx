import { useEffect, useRef, useState } from "preact/hooks";
import { createEditor, type MarkdownEditor } from "mde";
import "mde/style.css";
import { AccessLevel, api, errorMessage, type Task } from "../api";
import { renderMarkdown } from "../markdown";

export const canDo = (t: Task) => t.myAccess >= AccessLevel.DO;
export const canEdit = (t: Task) => t.myAccess === AccessLevel.FULL;

/** The checklist items still in use. */
export const activeChecklist = (t: Task) => t.checklist.filter((i) => !i.removed);
export const activeSlots = (t: Task) => t.slots.filter((s) => !s.removed);

export function slotTitle(t: Task, id: string): string {
  return t.slots.find((s) => s.id === id)?.title ?? "";
}

export function Markdown({ text, class: cls }: { text: string; class?: string }) {
  if (!text.trim()) return null;
  return <div class={`markdown ${cls ?? ""}`} dangerouslySetInnerHTML={{ __html: renderMarkdown(text) }} />;
}

/** mde, without images or video. */
export function MarkdownField({ value, onChange, placeholder }: { value: string; onChange: (md: string) => void; placeholder?: string }) {
  const host = useRef<HTMLDivElement>(null);
  const editor = useRef<MarkdownEditor | null>(null);
  const changed = useRef(onChange);
  changed.current = onChange;
  useEffect(() => {
    editor.current = createEditor(host.current!, {
      markdown: value,
      placeholder,
      images: false,
      videos: false,
      modes: ["rich", "source"],
      onChange: (md) => changed.current(md),
    });
    return () => editor.current?.destroy();
    // The editor owns its content after mount.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return <div class="mde-host" ref={host} />;
}

export function ErrorBanner({ error, onDismiss }: { error: string; onDismiss?: () => void }) {
  if (!error) return null;
  return (
    <div class="error" role="alert">
      {error}
      {onDismiss && (
        <button class="link" onClick={onDismiss} aria-label="Dismiss">
          ✕
        </button>
      )}
    </div>
  );
}

/** Runs an async operation, tracking busy state and turning failures into messages. */
export function useRunner() {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  async function run<T>(fn: () => Promise<T>, onError?: (err: unknown) => void): Promise<T | undefined> {
    setBusy(true);
    setError("");
    try {
      return await fn();
    } catch (err) {
      setError(errorMessage(err));
      onError?.(err);
      return undefined;
    } finally {
      setBusy(false);
    }
  }
  return { busy, error, setError, run };
}

// ---- Toasts ----

type ToastAction = { label: string; run: () => Promise<unknown> };
type ToastMsg = { id: number; text: string; action?: ToastAction };
let toastListener: ((t: ToastMsg) => void) | null = null;
let nextToast = 1;

/** Shows a brief message, optionally with a button (like Undo). */
export function toast(text: string, action?: ToastAction) {
  toastListener?.({ id: nextToast++, text, action });
}

/** An Undo button for a toast after an action that returned `task`. */
export function undoAction(task: Task, onUndone: (t: Task) => void): ToastAction {
  return {
    label: "Undo",
    run: async () => {
      try {
        const res = await api.undo({ id: task.id, version: task.version });
        if (res.task) onUndone(res.task);
        toast("Undone");
      } catch (err) {
        toast(errorMessage(err));
      }
    },
  };
}

export function Toasts() {
  const [items, setItems] = useState<ToastMsg[]>([]);
  useEffect(() => {
    toastListener = (t) => {
      setItems((list) => [...list, t]);
      setTimeout(() => setItems((list) => list.filter((x) => x.id !== t.id)), t.action ? 8000 : 4000);
    };
    return () => {
      toastListener = null;
    };
  }, []);
  return (
    <div class="toasts" aria-live="polite">
      {items.map((t) => (
        <div class="toast" key={t.id}>
          <span>{t.text}</span>
          {t.action && (
            <button
              class="toast-action"
              onClick={() => {
                setItems((list) => list.filter((x) => x.id !== t.id));
                t.action!.run();
              }}
            >
              {t.action.label}
            </button>
          )}
        </div>
      ))}
    </div>
  );
}

/** Reloads when the page becomes visible again and every few minutes. */
export function useAutoRefresh(reload: () => void, everyMs = 5 * 60_000) {
  const fn = useRef(reload);
  fn.current = reload;
  useEffect(() => {
    const onVisible = () => document.visibilityState === "visible" && fn.current();
    document.addEventListener("visibilitychange", onVisible);
    const timer = setInterval(() => document.visibilityState === "visible" && fn.current(), everyMs);
    return () => {
      document.removeEventListener("visibilitychange", onVisible);
      clearInterval(timer);
    };
  }, [everyMs]);
}
