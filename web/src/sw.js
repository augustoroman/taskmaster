// Taskmaster service worker: makes the app open offline and (see below)
// shows push notifications. Plain JS; the build (vite.config.ts) fills in
// the list of files to cache and a version.

const VERSION = "__VERSION__";
const PRECACHE = __PRECACHE__;
const CACHE = `taskmaster-${VERSION}`;
const SHELL = "/"; // cached index.html, used for navigations when offline

self.addEventListener("install", (event) => {
  event.waitUntil(
    (async () => {
      const cache = await caches.open(CACHE);
      await cache.addAll([SHELL, ...PRECACHE]);
      await self.skipWaiting();
    })(),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil(
    (async () => {
      for (const key of await caches.keys()) {
        if (key.startsWith("taskmaster-") && key !== CACHE) await caches.delete(key);
      }
      await self.clients.claim();
    })(),
  );
});

self.addEventListener("fetch", (event) => {
  const req = event.request;
  const url = new URL(req.url);
  if (req.method !== "GET" || url.origin !== self.location.origin) return; // API calls go straight through

  if (req.mode === "navigate") {
    // Network first so logins and updates work; the cached app when offline.
    event.respondWith(
      (async () => {
        try {
          const res = await fetchWithTimeout(req, 5000);
          if (res.ok && res.type === "basic") (await caches.open(CACHE)).put(SHELL, res.clone());
          return res;
        } catch {
          return (await caches.match(SHELL)) ?? Response.error();
        }
      })(),
    );
    return;
  }

  // Built assets and icons: cache first.
  event.respondWith(
    (async () => {
      const cached = await caches.match(req);
      if (cached) return cached;
      const res = await fetch(req);
      if (res.ok && (url.pathname.startsWith("/assets/") || url.pathname.startsWith("/icons/"))) {
        (await caches.open(CACHE)).put(req, res.clone());
      }
      return res;
    })(),
  );
});

function fetchWithTimeout(req, ms) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error("timeout")), ms);
    fetch(req).then(
      (res) => (clearTimeout(timer), resolve(res)),
      (err) => (clearTimeout(timer), reject(err)),
    );
  });
}

// ---- Push notifications ----
// The server sends app.Notification payloads as JSON.

self.addEventListener("push", (event) => {
  let n = {};
  try {
    n = event.data ? event.data.json() : {};
  } catch {
    n = { title: "Taskmaster", body: event.data?.text() ?? "" };
  }
  const options = {
    body: n.body || "",
    tag: n.tag || undefined,
    data: n,
    icon: "/icons/icon-192.png",
    badge: "/icons/badge-96.png",
  };
  if (n.type === "task" && n.actions) {
    options.actions = [
      { action: "done", title: "Done" },
      { action: "tomorrow", title: "Tomorrow" },
    ];
  }
  event.waitUntil(self.registration.showNotification(n.title || "Taskmaster", options));
});

self.addEventListener("notificationclick", (event) => {
  const n = event.notification.data || {};
  event.notification.close();
  event.waitUntil(
    (async () => {
      if (event.action === "done") {
        const occurrence = n.occurrence;
        const date = localDate();
        const ok = await call("Complete", { id: n.task_id, offline: { occurrence, date } });
        if (ok === "offline") {
          // Sent when the app next opens with a connection (see offline.ts).
          await queue({ kind: "complete", taskId: n.task_id, taskTitle: n.title, occurrence, date, queuedAt: Date.now() });
        }
        if (ok) return;
      } else if (event.action === "tomorrow") {
        if ((await call("Defer", { id: n.task_id, to: n.tomorrow })) === true) return;
      }
      await openApp(n.url || "/");
    })(),
  );
});

/** Calls the API; returns true, "offline", or false (other failure). */
async function call(method, body) {
  try {
    const res = await fetch(`/taskmaster.v1.TaskmasterService/${method}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      credentials: "include",
      body: JSON.stringify(body),
    });
    return res.ok;
  } catch {
    return "offline";
  }
}

async function openApp(url) {
  const windows = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
  for (const w of windows) {
    if ("focus" in w) {
      await w.focus();
      if ("navigate" in w) await w.navigate(url).catch(() => {});
      return;
    }
  }
  await self.clients.openWindow(url);
}

function localDate() {
  const d = new Date();
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

/** Adds an action to the app's offline queue (IndexedDB "taskmaster"/"queue"). */
function queue(item) {
  return new Promise((resolve) => {
    const open = indexedDB.open("taskmaster", 1);
    open.onupgradeneeded = () => {
      open.result.createObjectStore("cache");
      open.result.createObjectStore("queue", { keyPath: "id", autoIncrement: true });
    };
    open.onsuccess = () => {
      const tx = open.result.transaction("queue", "readwrite");
      tx.objectStore("queue").add(item);
      tx.oncomplete = tx.onerror = () => resolve();
    };
    open.onerror = () => resolve();
  });
}
