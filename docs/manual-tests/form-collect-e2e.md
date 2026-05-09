# Form Collect e2e

End-to-end smoke for `tangent.form-collect`.

## Prerequisites

- `make build` produced `./tangent`.
- Tangent is registered in Claude Code or another MCP client.

## 1. Boot Tangent

```bash
cd ~/Projects-apps/tangent
./tangent
```

## 2. Trigger a form-collect call

Ask Claude Code to call `tangent.form-collect` with:

- one required text field
- one conditional checkbox/section pair
- one submit action
- one attachment ref

## 3. Verify the browser flow

Open the `/r/<roomID>` URL and confirm:

- required fields gate submit
- conditional sections appear deterministically
- repeatable rows can be added
- refresh recovers unsent local state
- saved drafts/templates can be restored in the same room

## 4. Verify the MCP response

After submit, the tool response should include:

```json
{
  "payload": {
    "form_id": "<id>",
    "answers": {},
    "notes": "",
    "action_id": "<optional>",
    "attachment_refs": []
  }
}
```

## Mock equivalent

```bash
node scripts/form-collect-mock-call.mjs
```
