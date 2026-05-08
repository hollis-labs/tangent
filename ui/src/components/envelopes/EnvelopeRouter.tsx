// EnvelopeRouter is the type→component dispatcher. PR 4 ships the
// placeholder: render the raw envelope JSON in a <pre>. PR 5
// replaces this with a real component map (info-card, triage,
// confirmation-card, etc).
//
// The component is intentionally dumb — it does NOT submit, cancel,
// or otherwise interact with the WS layer. The owning Room component
// passes those callbacks down so this file stays free of imperative
// state.

type Props = {
  envelope: unknown;
};

export function EnvelopeRouter({ envelope }: Props) {
  const text = JSON.stringify(envelope, null, 2);
  return (
    <pre className="whitespace-pre-wrap break-words rounded border border-zinc-800 bg-zinc-900 p-3 text-xs text-zinc-200">
      {text}
    </pre>
  );
}
