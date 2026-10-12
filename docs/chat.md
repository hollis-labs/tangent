# Chat tab

`/chat` is a chat-room view over the same Agent Turns data the Inbox reads
(`GET /api/turns`). The Inbox is unchanged; Chat shares no state or markup with
it and is a new view with its own semantics.

- **Placement.** Core UI, not a plugin-contributed tab: the host has no plugin
  UI contribution point for top-level tabs, and Chat adds no application
  dependency or write path of its own. It reads the existing turns API and
  replies through the existing `POST /api/turns/items/{id}/reply`.
- **Chat agents.** The operator picks which agents appear (sidebar "Add
  agent"). The roster, pins, per-agent last-seen time and page size are
  per-browser view state in `localStorage` (`tangent.chat.prefs.v1`). A plugin
  setting that names chat agents is a follow-up.
- **Stream.** An item is the agent's message; its resolution's `response_text`
  is the operator's. They are merged by timestamp and de-duplicated by item id
  and resolution id. Only the last N (default 50, configurable) are shown;
  scrolling to the top or "Load older messages" reveals more.
- **Composer.** Sends action `respond` against the newest pending, replyable
  final/question/failure of that agent with its current revision. With none
  pending the composer is disabled ("is working"). A 409 keeps the draft and
  says the agent moved on, so nothing is injected twice.
- **Display.** No title header and no summary. Trace, delivery and source
  publication are in the (i) modal.
