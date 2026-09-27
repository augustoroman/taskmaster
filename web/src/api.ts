import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { TaskmasterService } from "./gen/taskmaster/v1/taskmaster_pb.js";

export * from "./gen/taskmaster/v1/taskmaster_pb.js";

export const api = createClient(
  TaskmasterService,
  createConnectTransport({ baseUrl: window.location.origin }),
);

/** A user-facing message for an API error. */
export function errorMessage(err: unknown): string {
  if (err instanceof ConnectError) {
    switch (err.code) {
      case Code.Unauthenticated:
        return "Your login has expired. Reload the page to log in again.";
      case Code.Aborted:
        return "Someone else changed this task. It has been reloaded; try again.";
      case Code.NotFound:
        return "Not found. It may have been deleted, or it's no longer shared with you.";
      default:
        return err.rawMessage;
    }
  }
  return err instanceof Error ? err.message : String(err);
}

export function isConflict(err: unknown): boolean {
  return err instanceof ConnectError && err.code === Code.Aborted;
}
