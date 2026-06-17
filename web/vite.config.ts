import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// Vite config: build output goes to web/dist/ (matches Go's embed pattern).
// During `vite dev`, the dev server runs on :5173 with a proxy to the Go
// backend on :5000 for /api and /healthz.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "dist",
    emptyOutDir: true,
    sourcemap: true,
  },
  server: {
    port: 5173,
    proxy: {
      "/api": "http://127.0.0.1:5000",
      "/healthz": "http://127.0.0.1:5000",
      "/ws": {
        target: "ws://127.0.0.1:5000",
        ws: true,
      },
    },
  },
});
