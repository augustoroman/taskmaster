import { defineConfig } from "vite";
import preact from "@preact/preset-vite";

// In development, run the Go server (TASKS_DEV_USER=... go run ./cmd/taskmaster)
// and `npm run dev`; API calls are proxied to it.
export default defineConfig({
  plugins: [preact()],
  // public/.gitkeep keeps dist/ present so the Go embed compiles before a build.
  // The bundle is mostly ProseMirror (mde); fine for this app.
  build: { outDir: "dist", emptyOutDir: true, chunkSizeWarningLimit: 800 },
  server: {
    proxy: { "/taskmaster.v1.TaskmasterService": "http://localhost:8080" },
  },
});
