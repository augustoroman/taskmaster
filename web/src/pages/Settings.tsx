import { useEffect, useState } from "preact/hooks";
import { api } from "../api";
import { ErrorBanner, toast, useRunner } from "../components/common";
import { deviceState, disableOnDevice, enableOnDevice, type DeviceState } from "../notifications";
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
    <>
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
    <Notifications />
    </>
  );
}

const DEVICE_TEXT: Record<DeviceState, string> = {
  unsupported: "This browser can't show notifications.",
  "needs-install": "On iPhone and iPad, first add Taskmaster to your Home Screen (Share → Add to Home Screen), then open it from there.",
  denied: "Notifications are blocked for this site. Allow them in your browser or system settings, then reload.",
  off: "Not turned on for this device.",
  on: "On for this device.",
};

function Notifications() {
  const session = useSession();
  const me = session.me;
  const [device, setDevice] = useState<DeviceState | null>(null);
  const [time, setTime] = useState(me.notifyTime || "08:00");
  const { busy, error, setError, run } = useRunner();

  useEffect(() => {
    deviceState().then(setDevice);
  }, []);

  async function update(patch: { notify?: boolean; notifyTime?: string }) {
    const res = await run(() => api.updateMe(patch));
    if (res?.user) session.setMe(res.user);
  }
  async function device_(fn: () => Promise<DeviceState>) {
    const state = await run(fn);
    if (state) setDevice(state);
    const res = await api.getMe({}).catch(() => null);
    if (res?.user) session.setMe(res.user);
  }

  return (
    <section class="settings notifications">
      <h2>Notifications</h2>
      <p class="muted small">
        Each morning, a notification for each task due today or overdue (at most 4), plus a week's notice for tasks that come
        up every 6 months or less often. Choose which tags notify you on the Tags page.
      </p>
      <label class="check">
        <input type="checkbox" checked={me.notify} disabled={busy} onChange={(e) => update({ notify: e.currentTarget.checked })} />
        Send me notifications
      </label>
      {me.notify && (
        <label>
          At
          <input
            type="time"
            value={time}
            disabled={busy}
            onInput={(e) => setTime(e.currentTarget.value)}
            onChange={(e) => update({ notifyTime: e.currentTarget.value })}
          />
          <span class="muted small">In your time zone ({me.timeZone}).</span>
        </label>
      )}
      <h3>This device</h3>
      <p class="muted small">
        {device ? DEVICE_TEXT[device] : "Checking…"}
        {me.pushDevices > 0 && ` You get notifications on ${me.pushDevices} device${me.pushDevices === 1 ? "" : "s"}.`}
      </p>
      <div class="button-row">
        {device === "off" && (
          <button class="primary" disabled={busy} onClick={() => device_(enableOnDevice)}>
            Turn on for this device
          </button>
        )}
        {device === "on" && (
          <>
            <button
              disabled={busy}
              onClick={async () => {
                const res = await run(() => api.sendTestNotification({}));
                if (res) toast(`Test sent to ${res.delivered} device${res.delivered === 1 ? "" : "s"}`);
              }}
            >
              Send a test notification
            </button>
            <button class="link" disabled={busy} onClick={() => device_(disableOnDevice)}>
              Turn off for this device
            </button>
          </>
        )}
      </div>
      <ErrorBanner error={error} onDismiss={() => setError("")} />
    </section>
  );
}
