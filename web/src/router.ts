import { useEffect, useState } from "preact/hooks";

export type Route =
  | { page: "upcoming" }
  | { page: "tasks" }
  | { page: "task"; id: string }
  | { page: "edit"; id: string }
  | { page: "new" }
  | { page: "tags" }
  | { page: "settings" };

export function parseRoute(hash: string): Route {
  const parts = hash.replace(/^#\/?/, "").split("/").filter(Boolean).map(decodeURIComponent);
  switch (parts[0]) {
    case "tasks":
      return { page: "tasks" };
    case "task":
      if (parts[1]) return parts[2] === "edit" ? { page: "edit", id: parts[1] } : { page: "task", id: parts[1] };
      break;
    case "new":
      return { page: "new" };
    case "tags":
      return { page: "tags" };
    case "settings":
      return { page: "settings" };
  }
  return { page: "upcoming" };
}

export const href = {
  upcoming: () => "#/",
  tasks: () => "#/tasks",
  task: (id: string) => `#/task/${encodeURIComponent(id)}`,
  edit: (id: string) => `#/task/${encodeURIComponent(id)}/edit`,
  newTask: () => "#/new",
  tags: () => "#/tags",
  settings: () => "#/settings",
};

export function navigate(to: string) {
  window.location.hash = to;
}

export function useRoute(): Route {
  const [route, setRoute] = useState(() => parseRoute(window.location.hash));
  useEffect(() => {
    const onChange = () => {
      setRoute(parseRoute(window.location.hash));
      window.scrollTo(0, 0);
    };
    window.addEventListener("hashchange", onChange);
    return () => window.removeEventListener("hashchange", onChange);
  }, []);
  return route;
}
