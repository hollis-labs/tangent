import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";

// Tangent v0.1 keeps the Vite config deliberately boring:
//   - React + Tailwind v4 plugins, nothing else.
//   - Build output is written to ../internal/server/ui_dist so the Go
//     `//go:embed all:ui_dist` directive in internal/server/static.go
//     can resolve it without any post-build copy step.
//   - Dev server runs on the Vite default (5173). The Go server, when
//     started with TANGENT_DEV_FRONTEND_URL=http://localhost:5173,
//     reverse-proxies non-API requests to it.
//
// The Nanite-style importmap / shared-chunk pattern is intentionally
// omitted: it exists to support runtime-loaded plugins, which Tangent
// does not (yet) have. Add it when a plugin system lands.
export default defineConfig({
  plugins: [react(), tailwindcss()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
  server: {
    port: 5173,
    strictPort: true,
  },
  build: {
    // Output directly into the Go embed target (see internal/server/static.go).
    // emptyOutDir is disabled so the committed `.gitkeep` placeholder
    // survives the build — without it `go build ./...` would fail on a
    // fresh checkout where ui_dist/ contained no files at all.
    outDir: path.resolve(__dirname, "../internal/server/ui_dist"),
    emptyOutDir: false,
  },
});
