import { createHash } from "node:crypto";
import { readFileSync } from "node:fs";
import { defineConfig, type Plugin } from "vite";
import preact from "@preact/preset-vite";

/**
 * Emits sw.js from src/sw.js with the list of built files to cache and a
 * version that changes whenever they do.
 */
function serviceWorker(): Plugin {
  return {
    name: "taskmaster-sw",
    apply: "build",
    generateBundle(_, bundle) {
      const files = Object.keys(bundle)
        .filter((f) => !f.endsWith(".map") && f !== "index.html")
        .map((f) => "/" + f)
        .concat(["/manifest.webmanifest", "/icons/icon-192.png"])
        .sort();
      const version = createHash("sha256").update(files.join("\n")).digest("hex").slice(0, 12);
      const source = readFileSync("src/sw.js", "utf8")
        .replace('"__VERSION__"', JSON.stringify(version))
        .replace("__PRECACHE__", JSON.stringify(files));
      this.emitFile({ type: "asset", fileName: "sw.js", source });
    },
  };
}

// In development, run the Go server (TASKS_DEV_USER=... go run ./cmd/taskmaster)
// and `npm run dev`; API calls are proxied to it.
export default defineConfig({
  plugins: [preact(), serviceWorker()],
  // public/.gitkeep keeps dist/ present so the Go embed compiles before a build.
  // The bundle is mostly ProseMirror (mde); fine for this app.
  build: { outDir: "dist", emptyOutDir: true, chunkSizeWarningLimit: 800 },
  server: {
    proxy: { "/taskmaster.v1.TaskmasterService": "http://localhost:8080" },
  },
});
