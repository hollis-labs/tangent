import { Markdown } from "@/components/markdown";
import type { TurnItemView } from "@/lib/turns-api";

// Shared by both inbox surfaces. Stage output is escaped plain text; it is
// never rendered as an executable option or trusted Markdown instruction.
export function TurnContent({ item }: { item: TurnItemView }) {
  const annotation = item.annotations?.find((value) => value.kind === "summary");
  const summary = annotation?.summary.text ?? item.summary;
  const failures = item.stage_trace?.filter((trace) => trace.outcome !== "passed") ?? [];
  const source = item.source_message;
  return (
    <section className="space-y-4">
      {summary ? (
        <section aria-label="Message summary" className="rounded border border-border p-3">
          <h3 className="mb-2 text-sm font-semibold">Summary</h3>
          <p className="whitespace-pre-wrap break-words">{summary}</p>
        </section>
      ) : null}
      {failures.map((trace) => (
        <p key={trace.stage_id} role="status" className="text-sm text-warning">
          Stage {trace.stage_id} {trace.outcome === "timed_out" ? "timed out" : "failed"}:{" "}
          {trace.failure_code}
        </p>
      ))}
      {annotation ? (
        <details className="rounded border border-border p-3">
          <summary className="cursor-pointer font-semibold">Original message</summary>
          <pre className="mt-3 whitespace-pre-wrap break-words font-sans">{item.content}</pre>
        </details>
      ) : (
        <Markdown content={item.content} />
      )}
      {item.stage_trace?.length ? (
        <details className="text-sm">
          <summary className="cursor-pointer">Stage trace</summary>
          <ul className="mt-2 space-y-1">
            {item.stage_trace.map((trace) => (
              <li key={trace.stage_id}>
                {trace.stage_id} ({trace.stage_version}): {trace.outcome}, {trace.duration_ms} ms
                {trace.failure_code ? ` · ${trace.failure_code}` : ""}
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {source ? (
        <details className="text-sm">
          <summary className="cursor-pointer">Source publication</summary>
          <dl className="mt-2 space-y-1 break-words">
            <dt>Sender attribution (unverified)</dt>
            <dd>{source.sender_urn}</dd>
            <dt>Origin</dt>
            <dd>{source.origin}</dd>
            <dt>Endpoint reference</dt>
            <dd>{source.endpoint_ref}</dd>
            <dt>Channel</dt>
            <dd>{source.channel}</dd>
            <dt>Publication</dt>
            <dd>
              {source.message_id} · sequence {source.sequence}
            </dd>
            {source.output_id ? (
              <>
                <dt>Output</dt>
                <dd>{source.output_id}</dd>
              </>
            ) : null}
            {source.attribution?.runtime ? (
              <>
                <dt>Runtime</dt>
                <dd>{source.attribution.runtime}</dd>
              </>
            ) : null}
            {source.attribution?.confidence ? (
              <>
                <dt>Text extraction confidence</dt>
                <dd>{source.attribution.confidence}</dd>
              </>
            ) : null}
          </dl>
        </details>
      ) : null}
    </section>
  );
}
