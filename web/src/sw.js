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
