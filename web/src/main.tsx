import { render } from "preact";
import "./style.css";
import { SessionProvider } from "./session";
import { href, useRoute, type Route } from "./router";
import { Upcoming } from "./pages/Upcoming";
import { AllTasks } from "./pages/AllTasks";
import { TaskDetail } from "./pages/TaskDetail";
import { TaskEdit } from "./pages/TaskEdit";
import { Tags } from "./pages/Tags";
import { Settings } from "./pages/Settings";
import { Toasts } from "./components/common";
import { OfflineBanner } from "./components/OfflineBanner";
import { startSync } from "./offline";

function Page({ route }: { route: Route }) {
  switch (route.page) {
    case "upcoming":
      return <Upcoming />;
    case "tasks":
      return <AllTasks />;
    case "task":
      return <TaskDetail key={route.id} id={route.id} />;
    case "edit":
      return <TaskEdit key={route.id} id={route.id} />;
    case "new":
      return <TaskEdit key="new" />;
    case "tags":
      return <Tags />;
    case "settings":
      return <Settings />;
  }
}

function App() {
  const route = useRoute();
  const tab = (page: Route["page"], to: string, label: string) => (
    <a href={to} class={route.page === page ? "active" : ""}>
      {label}
    </a>
  );
  return (
    <>
      <header class="topbar">
        <nav>
          {tab("upcoming", href.upcoming(), "Upcoming")}
          {tab("tasks", href.tasks(), "All tasks")}
          {tab("tags", href.tags(), "Tags")}
          {tab("settings", href.settings(), "Settings")}
        </nav>
        <a class="button primary new" href={href.newTask()} aria-label="New task">
          +<span class="new-label"> New</span>
        </a>
      </header>
      <main class="page">
        <OfflineBanner />
        <Page route={route} />
      </main>
      <Toasts />
    </>
  );
}

if (import.meta.env.PROD && "serviceWorker" in navigator) {
  navigator.serviceWorker.register("/sw.js").catch((err) => console.warn("service worker:", err));
}

startSync();

render(
  <SessionProvider>
    <App />
  </SessionProvider>,
  document.getElementById("app")!,
);
