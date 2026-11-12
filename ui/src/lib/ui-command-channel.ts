// Whether the page opens the UI-command channel on a room's socket.
//
// The host does not switch the channel on yet (CW-20261112-0128): it answers
// every `view.active` frame with `ui_command_rejected`, so the page would send
// frames it can never use and show "host refused channel" on every room open.
// The old build did the same. While this is false, `Room` does not attach the
// channel: no view frames go out and the inbox header says "unavailable". Set it
// true when the host takes the channel; the hook and its tests are untouched.
export const UI_COMMAND_CHANNEL_ENABLED = false;
