---
name: tangent-artifacts
description: Use when showing the operator a mock, wireframe, diagram, screenshot, PDF or chart through Tangent's `tangent_artifact_*` tools. Covers which type to send and how to build a shadcn/ui React mock that runs inside the artifact sandbox.
---

# Tangent artifacts

`tangent_artifact_create` puts one durable item in the operator's Inbox. Update it with
`tangent_artifact_update` (a new revision of the same item) instead of creating a stream of
new ones. Read replies with `tangent_artifact_get`, or block with `tangent_artifact_await`.
Pass the same `source` (`application_id` and `agent_id`) on every follow-up call.
Design: `docs/adr/0015-artifact-inbox-kind.md`.

## Pick the type

| You have | Send |
|---|---|
| A UI mock or wireframe | `html` (one self-contained file) |
| A box-and-arrow diagram | `excalidraw` (sketchy), `mermaid`, `d2`, or `flow` (node graph) |
| A chart | `vega-lite` (inline data only) |
| A screenshot or picture | `image` (`content_base64`) |
| A document or recording | `pdf`, `video`, `markdown` |
| A design in Figma | `figma` (a figma.com URL) |

Text types go in `content`, binary types in `content_base64`.

### Content formats and limits

| Type | Send |
|---|---|
| `html` | One complete HTML document. |
| `markdown` | Markdown text. |
| `image` | PNG, JPEG, GIF or WebP as base64 in `content_base64`; SVG as text in `content`. |
| `excalidraw` | JSON `{"elements": [...]}`, an Excalidraw scene. |
| `flow` | JSON `{"direction": "LR"\|"TB", "nodes": [{"id", "label", "kind"?}], "edges": [{"source", "target", "label"?}]}`. `kind` is trigger, action, condition, output or note. At most 500 nodes and 2000 edges; every edge joins two node ids. |
| `mermaid` | Mermaid source. |
| `d2` | D2 source. No `@file` imports; the diagram must compile or the call is refused with d2's message. |
| `vega-lite` | A Vega-Lite spec with inline `data.values`. A `url` anywhere in the spec is refused. |
| `pdf` | Bytes as base64. |
| `video` | mp4 or webm bytes as base64. |
| `figma` | A figma.com URL. |

Limits: 5 MiB for text types, 25 MiB for binary types. The same text is in the
`type` field's description on `tangent_artifact_create`; the server is the authority.

### Closing tags through the gateway (fixed, legacy note)

Until 2026-10-25 the Tether gateway's sanitizer stripped a trailing closing tag from long content, so
a bare `</svg>` or `</html>` at the very end was lost and an SVG showed a broken preview
(CW-20261024-0130). Fixed in libs `mcp/plugin-mcp` v0.6.0 and live in Tether; verified through the
gateway with a 114-byte SVG and a 277-byte HTML page, both stored intact with no trailing comment. The
old workaround (`</svg><!-- end -->`) is harmless but no longer needed for Tangent tools. It still
applies to anything routed through Tesseract or Torque until they bump the library, and artifacts
created before the fix may be missing their last tag.

### Calls and privacy

- `idempotency_key` is yours; repeating it returns the original item without comparing the content.
- Follow-up calls need the same `source.application_id`. Artifacts are private to that
  application (or to the verified gateway identity); `agent_id` is only the author shown to the operator.
- `requires_signoff: true` asks the operator to approve. Approve resolves the item; Request changes
  leaves it open and arrives as a message (read it with `tangent_artifact_get` or wait with `tangent_artifact_await`).

## shadcn/ui mocks

An `html` artifact runs in a sandboxed iframe: scripts, styles, fonts and images may load
from any `https:` origin, and the page cannot call out (`connect-src 'none'`), read cookies
or storage, or submit forms. There is no build step and no `@/` alias, so a shadcn mock is
**one HTML file** that inlines the component source.

1. **Find components.** The shadcn MCP tools are on the Tether gateway. Pass the registry
   explicitly, because the server has no `components.json`:
   - `search_items_in_registries` with `{"query": "card", "registries": ["@shadcn"]}`
   - `view_items_in_registries` with `{"items": ["@shadcn/card"]}` for the item's type and dependencies
   - `get_item_examples_from_registries` with `{"query": "card-demo", "registries": ["@shadcn"]}` for full source and a usage example

   Ignore the "Add command" line in results (it prints `[object Promise]`); you are not
   running the CLI.
2. **Start from the template** `.agents/skills/tangent-artifacts/shadcn-mock.template.html`.
   It already carries: Tailwind v4 (`@tailwindcss/browser`), the shadcn neutral theme tokens
   (`:root` variables and `@theme inline`), React 19 and `class-variance-authority`, `clsx`
   and `tailwind-merge` through an import map on esm.sh, Babel standalone for JSX, `cn()`,
   and registry `Button` and `Card` as worked examples.
3. **Inline each component you use**, copied from the registry source with these edits:
   drop the TypeScript types, drop `"use client"`, replace `@/lib/utils` and `@/registry/...`
   imports with the definitions already in the page, and replace `Slot`/`asChild` with a
   plain element. Keep the class strings unchanged; that is what makes it look like shadcn.
4. **Radix-based components** (dialog, dropdown, tabs, select, tooltip, popover) need their
   Radix package in the import map, for example
   `"@radix-ui/react-tabs": "https://esm.sh/@radix-ui/react-tabs@1?external=react,react-dom"`.
   Check that the mock renders before relying on one; prefer plain HTML for a static mock.
5. **Icons**: import from `https://esm.sh/lucide-react@0.469.0?external=react`, or inline SVG.
6. **Images** must be `https:` or `data:` URLs. Use `data:` for anything you generated.
7. **Pin versions** in every CDN URL. A mock should look the same next week.
8. **Dark mode**: the template defines only the light tokens. Add the `.dark` block from the
   shadcn theme if the operator asks for it.

Keep the mock a visual: no real fetches, no secrets, no real credentials in sample data.
Tailwind and Babel print two console warnings about production use; that is expected.

## Check before you send

Open the HTML yourself if you can (a local file is fine). The mock has loaded correctly if
the root element has children and the browser console shows only those two warnings.
