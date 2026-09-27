import { flushQueue, unqueue, useConnectivity } from "../offline";

/** Shows when the app is offline or has actions waiting to sync or that failed. */
export function OfflineBanner() {
  const { offline, savedAt, queue, failed } = useConnectivity();
  if (!offline && !queue.length && !failed.length) return null;
  const time = savedAt ? new Date(savedAt).toLocaleString(undefined, { weekday: "short", hour: "numeric", minute: "2-digit" }) : "";
  const waiting = queue.length ? ` ${queue.length} change${queue.length === 1 ? "" : "s"} waiting to sync.` : "";
  return (
    <div class="offline-banner" role="status">
      {offline ? (
        <p>
          <strong>Can't reach the server.</strong> Showing saved data{time ? ` from ${time}` : ""}.{waiting}{" "}
          <button class="link" onClick={() => window.location.reload()}>
            Reload
          </button>
        </p>
      ) : (
        queue.length > 0 && (
          <p>
            {waiting.trim()}{" "}
            <button class="link" onClick={() => flushQueue()}>
              Sync now
            </button>
          </p>
        )
      )}
      {failed.map((a) => (
        <p key={a.id} class="failed">
          Couldn't sync "{a.kind === "complete" ? "Done" : a.kind === "check" ? "Checked item" : "Note"}" for <strong>{a.taskTitle}</strong>: {a.error}{" "}
          <button class="link" onClick={() => unqueue(a.id!)}>
            Dismiss
          </button>
        </p>
      ))}
    </div>
  );
}
