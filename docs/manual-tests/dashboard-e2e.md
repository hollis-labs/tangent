# Dashboard e2e

Manual smoke for the dashboard workflow. This validates:

- room-backed dashboard reopen and explicit refresh/update submits
- saved layout selection, rename, and local browser recovery
- drill-down affordances into room URLs and artifact references
- accepted snapshot/export metadata shown in the dashboard view

## Setup

- Run Tangent locally: `make dev`
- Use a browser against `http://127.0.0.1:7842`
- Add the MCP server to your client:

```sh
claude mcp add --transport http tangent http://127.0.0.1:7842/mcp
```

## Drive One Dashboard Turn

Open a dashboard workflow with one room-backed tile and one artifact-backed tile:

```json
{
  "dashboard_id": "ops-dashboard",
  "title": "Ops dashboard",
  "tiles": [
    {
      "tile_id": "tile-open",
      "kind": "room_count",
      "title": "Open rooms",
      "value": "4",
      "room_id": "room-123"
    },
    {
      "tile_id": "tile-review",
      "kind": "workflow_summary",
      "title": "Reviews waiting",
      "value": "2",
      "workflow": "tangent.diff-review",
      "artifact_ref": "artifact://review-002"
    }
  ],
  "layout": [
    { "tile_id": "tile-open", "x": 0, "y": 0, "w": 2, "h": 1 },
    { "tile_id": "tile-review", "x": 1, "y": 0, "w": 2, "h": 1 }
  ],
  "saved_layouts": [
    {
      "layout_id": "layout-default",
      "name": "Default",
      "is_default": true,
      "tiles": [
        { "tile_id": "tile-open", "x": 0, "y": 0, "w": 2, "h": 1 },
        { "tile_id": "tile-review", "x": 1, "y": 0, "w": 2, "h": 1 }
      ]
    }
  ]
}
```

In the room UI:

1. Rename the default layout.
2. Move one tile up or down.
3. Click `Save as new` and select the new saved layout.
4. Reload the browser tab before submitting and confirm the draft layout recovers.
5. Click the room drill-down link and confirm it opens `/r/room-123`.
6. Click the artifact action and confirm the artifact ref is copied.
7. Submit `Refresh` with a note.

## Verify Persistence

- Re-open the same dashboard in the same room.
- Confirm the renamed layout, the selected active layout, and the tile ordering persisted.
- Confirm `snapshot_history` includes the accepted refresh.
- Confirm the export/share section shows the latest snapshot id, active layout id, and room/artifact refs.

## Constraints

- Dashboard drill-down stays inside existing Tangent room routes and artifact refs. Tangent publishes no external share links.
- Saved layouts are room-scoped and local-browser draft recovery is single-user, localhost only.
