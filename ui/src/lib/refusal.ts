// The Refused copy shape, with one owner.
//
// docs/room-validation-affordances.md fixes two surfaces and keeps them apart:
// **Blocked** is amber, `role="status"`, and means this tab has not produced a
// submittable answer yet; **Refused** is red, `role="alert"`, and means
// something was declined by a fact about the world that no amount of typing
// fixes. Refused copy always reads
//
//	Not submitted: <reason>. <what to do>.
//
// That document assigns the surface to `ConnectionStatus.describeServerError`
// and says an implementer must extend it rather than invent a second pattern.
// CW-20260825-0073 added a second *producer* of refusals — a renderer whose
// trust class this build will not dispatch, and a sandboxed payload over its
// declared limit — neither of which arrives through the WebSocket error
// channel `describeServerError` switches on. So the shape moved here, into a
// function both callers use, rather than the sentence being retyped in three
// files with three sets of punctuation.
//
// `describeServerError` still owns the *mapping* from wire code to copy. This
// owns only the sentence.

/**
 * Build one Refused line.
 *
 * `reason` is a lowercase clause naming what happened; `remedy` is a full
 * sentence naming the action that resolves it. Both are required: a refusal
 * with no remedy is a dead end, and the surface exists precisely because the
 * operator can do something about it.
 */
export function describeRefusal(reason: string, remedy: string): string {
  const trimmedReason = reason.trim().replace(/\.$/, "");
  const trimmedRemedy = remedy.trim();
  const punctuated = /[.!?]$/.test(trimmedRemedy) ? trimmedRemedy : `${trimmedRemedy}.`;
  return `Not submitted: ${trimmedReason}. ${punctuated}`;
}
