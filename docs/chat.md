# Chat tab

`/chat` is a chat-room view over the same Agent Turns data the Inbox reads
(`GET /api/turns`). The Inbox is unchanged; Chat shares no state or markup with
it and is a new view with its own semantics.

- **Placement.** Core UI, not a plugin-contributed tab: the host has no plugin
  UI contribution point for top-level tabs, and Chat adds no application
  dependency of its own. It reads the existing turns API, replies through the
  existing `POST /api/turns/items/{id}/reply`, and reaches Tether only through
  the messaging plugin's routes.
- **Chat agents.** The sidebar is the roster of agents the operator can chat
  with, and it comes from Tether: launches flagged `chat: true` there
  (`GET /chat/agents`), reached through the messaging plugin's
  `/api/plugins/messaging/chat/agents` route because Tangent core has no Tether
  client (ADR 0012). There is no add or remove control and no stored roster. Each
  row shows the display name, the last message time, an unread dot and a pin.
  Pins, per-agent last-seen time and page size stay per-browser view state in
  `localStorage` (`tangent.chat.prefs.v1`); a roster saved there by an earlier
  version is ignored. A turn belongs to an agent by the launch its publication
  names (`attribution.launch_id`), else by agent id.
- **Roster unavailable.** When the plugin is not installed or an older version,
  Tether predates the endpoint (404), the plugin's token is refused, or Tether
  cannot be reached, the view says "Chat roster unavailable", names the cause
  and offers Retry. An empty roster says no chat agents are configured on
  Tether. The roster refreshes on every turns revision and every 15 seconds.
- **Stream.** An item is the agent's message; its resolution's `response_text`
  is the operator's. They are merged by timestamp and de-duplicated by item id
  and resolution id. Only the last N (default 50, configurable) are shown;
  scrolling to the top or "Load older messages" reveals more.
- **Composer.** With a pending, replyable final/question/failure the existing
  reply path is used unchanged (action `respond`, the item's revision), so
  delivery tracking is kept. Otherwise the message goes through the plugin's
  `/api/plugins/messaging/chat/send` to Tether, which delivers it as the next
  turn of an idle running session or starts the launch with the text as its first
  prompt, so a chat can be initiated with nothing pending. While the agent's
  session is mid-turn the composer is disabled ("is working…"). A refusal (409
  reply conflict or `agent_working`) keeps the draft. Each distinct message text
  gets one idempotency key, reused on retry, so a lost response never sends it
  twice.
- **Display.** No title header and no summary. Trace, delivery and source
  publication are in the (i) modal.
