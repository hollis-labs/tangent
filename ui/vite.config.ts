import { createReadStream, readdirSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

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
// Excalidraw loads its hand-drawn fonts at runtime from EXCALIDRAW_ASSET_PATH. Tangent's CSP admits
// only its own origin, so the fonts are served from /excalidraw-assets/ instead of a CDN. Xiaolai (CJK,
// 12 MB) is left out; text in those scripts falls back to the system font.
const EXCALIDRAW_FONTS = path.resolve(
  __dirname,
  "node_modules/@excalidraw/excalidraw/dist/prod/fonts",
);
const SKIPPED_FONT_FAMILIES = new Set(["Xiaolai"]);

function listFontFiles(): { family: string; file: string }[] {
  const out: { family: string; file: string }[] = [];
  for (const family of readdirSync(EXCALIDRAW_FONTS)) {
    if (SKIPPED_FONT_FAMILIES.has(family)) continue;
    for (const file of readdirSync(path.join(EXCALIDRAW_FONTS, family))) out.push({ family, file });
  }
  return out;
}

function excalidrawFonts(): Plugin {
  return {
    name: "tangent-excalidraw-fonts",
    generateBundle() {
      for (const { family, file } of listFontFiles()) {
        this.emitFile({
          type: "asset",
          fileName: `excalidraw-assets/fonts/${family}/${file}`,
          source: readFileSync(path.join(EXCALIDRAW_FONTS, family, file)),
        });
      }
    },
    configureServer(server) {
      server.middlewares.use("/excalidraw-assets/fonts", (req, res, next) => {
        const target = path.join(
          EXCALIDRAW_FONTS,
          decodeURIComponent((req.url ?? "").split("?")[0]),
        );
        if (
          !target.startsWith(EXCALIDRAW_FONTS) ||
          !statSync(target, { throwIfNoEntry: false })?.isFile()
        )
          return next();
        res.setHeader("Content-Type", "font/woff2");
        createReadStream(target).pipe(res);
      });
    },
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), excalidrawFonts()],
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
    // emptyOutDir is enabled so each build produces a clean embed
    // directory — accumulated old hashed assets would otherwise inflate
    // the binary and risk serving stale files. The build-ui Make target
    // restores the committed `.gitkeep` placeholder after the build so
    // `go build ./...` continues to work on a fresh checkout where the
    // frontend hasn't been built yet.
    outDir: path.resolve(__dirname, "../internal/server/ui_dist"),
    emptyOutDir: true,
    rolldownOptions: {
      output: {
        // React and the router change far less often than app code. Their own
        // chunk keeps its content hash across app-only releases, so the
        // immutable Cache-Control on /assets/* keeps serving it from cache.
        codeSplitting: {
          groups: [
            {
              name: "vendor-react",
              test: /node_modules[\\/](react|react-dom|react-router|react-router-dom|scheduler)[\\/]/,
            },
          ],
        },
      },
    },
  },
});
