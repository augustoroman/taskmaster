// Offline support: saves API responses in IndexedDB so the app can show them
// without a connection, and queues a few actions (done, check item, note)
// to send when the connection comes back. See docs/design.md.

import { Code, ConnectError } from "@connectrpc/connect";
import { create, fromJson, toJson, type DescMessage, type MessageShape } from "@bufbuild/protobuf";
import { useEffect, useState } from "preact/hooks";
import { api, errorMessage, GetTaskResponseSchema, type Task } from "./api";

// ---- IndexedDB ----

const DB_NAME = "taskmaster";
let dbPromise: Promise<IDBDatabase> | null = null;

function db(): Promise<IDBDatabase> {
  dbPromise ??= new Promise((resolve, reject) => {
    const req = indexedDB.open(DB_NAME, 1);
    req.onupgradeneeded = () => {
      req.result.createObjectStore("cache");
      req.result.createObjectStore("queue", { keyPath: "id", autoIncrement: true });
    };
    req.onsuccess = () => resolve(req.result);
    req.onerror = () => reject(req.error);
  });
  return dbPromise;
}

async function run<T>(storeName: string, mode: IDBTransactionMode, fn: (s: IDBObjectStore) => IDBRequest<T>): Promise<T> {
  const tx = (await db()).transaction(storeName, mode);
  const req = fn(tx.objectStore(storeName));
  return new Promise((resolve, reject) => {
    tx.oncomplete = () => resolve(req.result);
    tx.onerror = () => reject(tx.error);
    tx.onabort = () => reject(tx.error);
  });
}

// ---- Connectivity state ----

interface State {
  /** The last request failed to reach the server. */
  offline: boolean;
  /** When the data being shown offline was saved (ms), if older. */
  savedAt: number;
  queue: QueuedAction[];
  failed: QueuedAction[];
}

let state: State = { offline: false, savedAt: 0, queue: [], failed: [] };
const listeners = new Set<(s: State) => void>();
const syncListeners = new Set<() => void>();

function setState(patch: Partial<State>) {
  state = { ...state, ...patch };
  listeners.forEach((l) => l(state));
}

export function useConnectivity(): State {
  const [s, set] = useState(state);
  useEffect(() => {
    listeners.add(set);
    return () => void listeners.delete(set);
  }, []);
  return s;
}

/** Calls fn after queued actions were sent, so pages can reload. */
export function useOnSynced(fn: () => void) {
  useEffect(() => {
    syncListeners.add(fn);
    return () => void syncListeners.delete(fn);
  }, [fn]);
}

/** Whether an error means the server couldn't be reached. */
export function isNetworkError(err: unknown): boolean {
  if (typeof navigator !== "undefined" && !navigator.onLine) return true;
  return err instanceof ConnectError && err.code === Code.Unknown && err.cause instanceof TypeError;
}

// ---- Cached reads ----

/**
 * Fetches a message, saving it for offline use. If the server can't be
 * reached, returns the saved copy (and marks the app offline).
 */
export async function cached<Desc extends DescMessage>(
  key: string,
  schema: Desc,
  fetch: () => Promise<MessageShape<Desc>>,
  /** Tasks in the response, also saved individually so they open offline. */
  tasksIn?: (msg: MessageShape<Desc>) => Task[],
): Promise<MessageShape<Desc>> {
  try {
    const msg = await fetch();
    if (state.offline) setState({ offline: false, savedAt: 0 });
    save(key, toJson(schema, msg));
    for (const task of tasksIn?.(msg) ?? []) {
      save(`task:${task.id}`, toJson(GetTaskResponseSchema, create(GetTaskResponseSchema, { task })));
    }
    if (state.queue.length) flushQueue();
    return msg;
  } catch (err) {
    if (!isNetworkError(err)) throw err;
    const saved = await run<{ json: unknown; savedAt: number } | undefined>("cache", "readonly", (s) => s.get(key)).catch(() => undefined);
    setState({ offline: true, savedAt: saved ? Math.min(state.savedAt || saved.savedAt, saved.savedAt) : state.savedAt });
    if (!saved) throw err;
    return fromJson(schema, saved.json as never, { ignoreUnknownFields: true });
  }
}

function save(key: string, json: unknown) {
  run("cache", "readwrite", (s) => s.put({ json, savedAt: Date.now() }, key)).catch(() => {});
}

/** Forgets saved data (e.g. on logout). */
export async function clearCache() {
  await run("cache", "readwrite", (s) => s.clear());
}

// ---- Queued actions ----

export type QueuedAction = {
  id?: number;
  kind: "complete" | "check" | "note" | "skip";
  taskId: string;
  taskTitle: string;
  itemId?: string;
  note?: string;
  /** Cycles: the slot it was done as. */
  asSlotId?: string;
  /** The occurrence it was for and the day it was done. */
  occurrence: string;
  date: string;
  queuedAt: number;
  error?: string;
};

async function loadQueue() {
  const all = await run<QueuedAction[]>("queue", "readonly", (s) => s.getAll()).catch(() => [] as QueuedAction[]);
  setState({ queue: all.filter((a) => !a.error), failed: all.filter((a) => a.error) });
}

/** Queues an action for a task, to send when back online. */
export async function queueAction(
  task: Task,
  today: string,
  action: Pick<QueuedAction, "kind" | "itemId" | "note" | "asSlotId">,
): Promise<QueuedAction> {
  const st = task.state!;
  const item: QueuedAction = {
    ...action,
    taskId: task.id,
    taskTitle: task.title,
    occurrence: st.deferred ? st.deferredFrom : st.due,
    date: today,
    queuedAt: Date.now(),
  };
  item.id = await run<IDBValidKey>("queue", "readwrite", (s) => s.add(item)) as number;
  await loadQueue();
  return item;
}

export async function unqueue(id: number) {
  await run("queue", "readwrite", (s) => s.delete(id));
  await loadQueue();
}

/** The queued (not yet sent) actions for a task. */
export function queuedFor(taskId: string, s: State = state): QueuedAction[] {
  return s.queue.filter((a) => a.taskId === taskId);
}

let flushing: Promise<void> | null = null;

/** Sends queued actions in order, stopping at the first network failure. */
export function flushQueue(): Promise<void> {
  flushing ??= (async () => {
    let sent = 0;
    try {
      for (const item of [...state.queue]) {
        try {
          await send(item);
          await run("queue", "readwrite", (s) => s.delete(item.id!));
          sent++;
        } catch (err) {
          if (isNetworkError(err)) break;
          await run("queue", "readwrite", (s) => s.put({ ...item, error: errorMessage(err) }));
        }
      }
    } finally {
      await loadQueue();
      flushing = null;
      if (sent) {
        setState({ offline: false, savedAt: 0 });
        syncListeners.forEach((l) => l());
      }
    }
  })();
  return flushing;
}

function send(a: QueuedAction): Promise<unknown> {
  const offline = { occurrence: a.occurrence, date: a.date };
  switch (a.kind) {
    case "complete":
      // Checklists are completed as they stand; offline, you can't see
      // whether someone else checked the rest.
      return api.complete({ id: a.taskId, note: a.note ?? "", asSlotId: a.asSlotId ?? "", force: true, offline });
    case "check":
      return api.checkItem({ id: a.taskId, itemId: a.itemId!, note: a.note ?? "", offline });
    case "note":
      return api.addNote({ taskId: a.taskId, note: a.note ?? "", date: a.date });
    case "skip":
      return api.skip({ id: a.taskId, note: a.note ?? "", offline });
  }
}

/** Starts loading the queue and sending it whenever the connection returns. */
export function startSync() {
  loadQueue().then(() => {
    if (state.queue.length) flushQueue();
  });
  window.addEventListener("online", () => flushQueue());
  document.addEventListener("visibilitychange", () => document.visibilityState === "visible" && state.queue.length && flushQueue());
  setInterval(() => state.queue.length && flushQueue(), 60_000);
}
