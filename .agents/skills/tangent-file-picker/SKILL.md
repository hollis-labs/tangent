---
name: tangent-file-picker
description: Use when launching Tangent's `tangent.file-picker` workflow. Provides a minimal copy/paste example and pointers to the deeper manual recipe, integration guide, and schema.
---

# Tangent File Picker

Use this skill when you need to launch `tangent.file-picker`.

## Copy/paste example

Use this exact payload shape:

```json
{
  "picker_id": "picker-manual-1",
  "browse_roots": [
    {
      "root_id": "workspace",
      "label": "Workspace",
      "path": "/tmp/workspace"
    }
  ],
  "files": [
    {
      "artifact_id": "artifact-spec",
      "name": "spec.md",
      "uri": "artifact://artifact-spec",
      "mime_type": "text/markdown",
      "root_id": "workspace",
      "relative_path": "docs/spec.md"
    },
    {
      "artifact_id": "artifact-readme",
      "name": "README.md",
      "uri": "artifact://artifact-readme",
      "mime_type": "text/markdown",
      "root_id": "workspace",
      "relative_path": "README.md"
    }
  ]
}
```

## Notes

- `browse_roots` alone is not enough. Seed `files` too.
- Use durable `artifact://...` refs.

## Pointers

- Manual recipe: `docs/manual-tests/file-picker-e2e.md`
- Smoke runbook: `docs/manual-tests/workflow-smoke-tests.md`
- Integration guide: `docs/mcp-integration.md`
- Tool schema: `internal/mcp/file_picker_schema.go`
- Handler: `internal/mcp/file_picker_handler.go`
