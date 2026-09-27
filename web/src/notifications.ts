// Push notifications on this device (see sw.js for how they're shown).

import { api } from "./api";

export type DeviceState = "unsupported" | "needs-install" | "denied" | "off" | "on";

const isIOS = () => /iPhone|iPad|iPod/.test(navigator.userAgent);
const isStandalone = () => window.matchMedia("(display-mode: standalone)").matches || ("standalone" in navigator && (navigator as { standalone?: boolean }).standalone === true);

export async function deviceState(): Promise<DeviceState> {
  if (!("serviceWorker" in navigator) || !("PushManager" in window) || !("Notification" in window)) {
    return isIOS() && !isStandalone() ? "needs-install" : "unsupported";
  }
  if (Notification.permission === "denied") return "denied";
  const reg = await navigator.serviceWorker.getRegistration();
  const sub = await reg?.pushManager.getSubscription();
  return sub && Notification.permission === "granted" ? "on" : "off";
}

function keyBytes(base64url: string): Uint8Array<ArrayBuffer> {
  const b64 = (base64url + "=".repeat((4 - (base64url.length % 4)) % 4)).replace(/-/g, "+").replace(/_/g, "/");
  const raw = atob(b64);
  const out = new Uint8Array(new ArrayBuffer(raw.length));
  for (let i = 0; i < raw.length; i++) out[i] = raw.charCodeAt(i);
  return out;
}

async function register(sub: PushSubscription) {
  const json = sub.toJSON();
  await api.registerPush({ endpoint: sub.endpoint, p256dh: json.keys?.p256dh ?? "", auth: json.keys?.auth ?? "" });
}

/** Asks for permission and subscribes this device. */
export async function enableOnDevice(): Promise<DeviceState> {
  if ((await Notification.requestPermission()) !== "granted") return deviceState();
  const { publicKey } = await api.getPushConfig({});
  if (!publicKey) throw new Error("This server isn't set up to send notifications.");
  const reg = await navigator.serviceWorker.ready;
  const sub =
    (await reg.pushManager.getSubscription()) ??
    (await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: keyBytes(publicKey) }));
  await register(sub);
  return "on";
}

export async function disableOnDevice(): Promise<DeviceState> {
  const reg = await navigator.serviceWorker.getRegistration();
  const sub = await reg?.pushManager.getSubscription();
  if (sub) {
    await api.unregisterPush({ endpoint: sub.endpoint }).catch(() => {});
    await sub.unsubscribe();
  }
  return deviceState();
}

/** Re-registers this device's subscription (it may have changed, or a different person logged in). */
export async function refreshRegistration() {
  if (!("serviceWorker" in navigator) || !("PushManager" in window)) return;
  const reg = await navigator.serviceWorker.getRegistration();
  const sub = await reg?.pushManager.getSubscription();
  if (sub) await register(sub).catch(() => {});
}

/** Closes a task's notification once it's been dealt with in the app. */
export async function closeTaskNotification(taskId: string) {
  if (!("serviceWorker" in navigator)) return;
  const reg = await navigator.serviceWorker.getRegistration();
  for (const n of (await reg?.getNotifications({ tag: `task-${taskId}` })) ?? []) n.close();
}
