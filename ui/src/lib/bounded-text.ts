// Display bounds shared by every renderer that shows agent-authored text.
//
// Two jobs, both about what reaches the DOM rather than what the agent sent:
// control characters are dropped so a terminal escape or a lone NUL cannot
// reach the browser as content, and the result is cut at a byte-ish ceiling so
// a runaway payload cannot hang a render. Callers pass their own maximum — the
// HITL drawer's is a published limit, an envelope pane's is a display choice —
// and are expected to surface `truncated` to the reader rather than silently
// dropping the tail.
export function boundedText(value: string, maximum: number): { text: string; truncated: boolean } {
  const sanitized = Array.from(value, (character) => {
    const code = character.charCodeAt(0);
    return (code >= 32 && code !== 127) || code === 9 || code === 10 || code === 13
      ? character
      : "";
  }).join("");
  if (sanitized.length <= maximum) return { text: sanitized, truncated: false };
  return { text: sanitized.slice(0, maximum), truncated: true };
}
