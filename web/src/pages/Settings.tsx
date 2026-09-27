import { useState } from "preact/hooks";
import { api } from "../api";
import { ErrorBanner, toast, useRunner } from "../components/common";
import { browserTimeZone } from "../dates";
import { useSession } from "../session";

function timeZones(): string[] {
  try {
    return Intl.supportedValuesOf("timeZone");
  } catch {
    return [];
  }
}

export function Settings() {
  const session = useSession();
  const [name, setName] = useState(session.me.name);
  const [tz, setTz] = useState(session.me.timeZone);
  const { busy, error, setError, run } = useRunner();
  const zones = timeZones();
  const browser = browserTimeZone();

  async function save(e: Event) {
    e.preventDefault();
    const res = await run(() => api.updateMe({ name, timeZone: tz }));
    if (res?.user) {
      session.setMe(res.user);
      toast("Saved");
    }
  }

  return (
    <form class="settings" onSubmit={save}>
      <h1>Settings</h1>
      <p class="muted">Logged in as {session.me.email}.</p>
      <label>
        Name
        <input type="text" value={name} onInput={(e) => setName(e.currentTarget.value)} />
      </label>
      <label>
        Time zone
        {zones.length ? (
          <select value={tz} onChange={(e) => setTz(e.currentTarget.value)}>
            {zones.map((z) => (
              <option key={z} value={z}>
                {z}
              </option>
            ))}
          </select>
        ) : (
          <input type="text" value={tz} onInput={(e) => setTz(e.currentTarget.value)} />
        )}
        <span class="muted small">
          New tasks use this time zone to decide when a day ends.
          {tz !== browser && (
            <>
              {" "}
              This browser is in {browser}.{" "}
              <button type="button" class="link" onClick={() => setTz(browser)}>
                Use that
              </button>
            </>
          )}
        </span>
      </label>
      <ErrorBanner error={error} onDismiss={() => setError("")} />
      <button class="primary" type="submit" disabled={busy}>
        Save
      </button>
    </form>
  );
}
