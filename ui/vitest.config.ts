/// <reference types="vitest" />
import path from "node:path";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

// Vitest config is split out from vite.config.ts so the production build
// (vite.config.ts) stays free of test-only deps. The two configs share
// only the React plugin and the @ alias — keeping them aligned with the
// app build is the only invariant test code cares about.
export default defineConfig({
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  test: {
    environment: "happy-dom",
    globals: true,
    setupFiles: ["./src/test-setup.ts"],
    include: ["src/**/*.{test,spec}.{ts,tsx}"],
  },
});
