import { ExternalLink, RefreshCw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Markdown } from "@/components/markdown";
import { Button } from "@/components/ui/button";
import type { TangentExternalReviewEnvelope } from "@/generated/envelope-types";
import type { EnvelopeComponentProps } from "@/lib/envelope-registry";

interface ReviewState {
  status: string;
  message: string;
  result?: Record<string, unknown>;
  complete?: boolean;
  disabled_actions?: string[];
}
const pluginPath = /^\/api\/plugins\/[a-z0-9][a-z0-9._-]*\/[a-z0-9][a-z0-9._/-]*$/;

// Only the plugin knows the application or what its actions do.
export function ExternalReview({
  envelope,
  roomID,
  onSubmit,
  onCancel,
  readOnly,
}: EnvelopeComponentProps) {
  const request = envelope as TangentExternalReviewEnvelope & { id: string };
  if (!request.data || !request.id) throw new Error("The review request is incomplete.");
  const data = request.data;
  const [state, setState] = useState<ReviewState | null>(null);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [options, setOptions] = useState<Record<string, string>>({});
  const lock = useRef(false);
  const sequence = useRef(0);
  const canCall = !!roomID && pluginPath.test(data.action_url) && pluginPath.test(data.state_url);
  const stateURL = data.state_url;
  const envelopeID = request.id;
  useEffect(() => {
    if (readOnly || !canCall) return;
    const current = ++sequence.current;
    const controller = new AbortController();
    void (async () => {
      try {
        const response = await fetch(stateURL, {
          method: "POST",
          credentials: "same-origin",
          signal: controller.signal,
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ room_id: roomID, envelope_id: envelopeID }),
        });
        const result = await response.json();
        if (!response.ok) throw new Error(result.message || "Could not refresh the review.");
        if (current === sequence.current) {
          setState(result);
          setError("");
        }
      } catch (reason) {
        if (!controller.signal.aborted && current === sequence.current)
          setError((reason as Error).message);
      }
    })();
    return () => {
      controller.abort();
      sequence.current++;
    };
  }, [canCall, readOnly, roomID, envelopeID, stateURL]);
  function finish(result: Record<string, unknown>) {
    onSubmit({
      v: 1,
      envelopeId: request.id,
      kind: "data",
      status: "submitted",
      payload: {
        ...result,
        review_id: data.review_id,
        resource_url: data.resource.url,
        resource_revision: data.resource.revision,
      },
    });
  }
  async function act(action?: string, option?: string) {
    if (lock.current || readOnly || !canCall) return;
    lock.current = true;
    setBusy(true);
    setError("");
    try {
      const response = await fetch(action ? data.action_url : data.state_url, {
        method: "POST",
        credentials: "same-origin",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          room_id: roomID,
          envelope_id: request.id,
          ...(action ? { action, option } : {}),
        }),
      });
      const next = (await response.json()) as ReviewState & { message: string };
      if (!response.ok) throw new Error(next.message || "The action failed. Try again.");
      setState(next);
      if (next.complete && action) finish(next.result || { outcome: next.status });
    } catch (reason) {
      setError((reason as Error).message);
    } finally {
      lock.current = false;
      setBusy(false);
    }
  }
  return (
    <article className="min-w-0 space-y-6">
      <header className="space-y-3">
        <div className="flex flex-wrap items-start gap-3">
          <h2 className="min-w-0 flex-1 text-xl font-semibold">{data.title}</h2>
          <a
            href={/^https:\/\//.test(data.resource.url) ? data.resource.url : undefined}
            target="_blank"
            rel="noopener noreferrer"
            className="inline-flex items-center gap-2 rounded border border-border px-3 py-2 text-sm"
          >
            <ExternalLink size={16} />
            Review on {data.resource.label || "source"}
          </a>
        </div>
        {data.fields?.length ? (
          <dl className="flex flex-wrap gap-x-6 gap-y-2 text-sm">
            {data.fields.map((field) => (
              <div key={field.label}>
                <dt className="text-fg-muted">{field.label}</dt>
                <dd className="break-all">{field.value}</dd>
              </div>
            ))}
          </dl>
        ) : null}
        <p className="break-all text-xs text-fg-muted">
          Reviewing revision {data.resource.revision}
        </p>
      </header>
      {data.summary ? (
        <section>
          <h3 className="mb-2 font-medium">Agent summary</h3>
          <Markdown content={data.summary} />
        </section>
      ) : null}
      {data.notes ? (
        <section>
          <h3 className="mb-2 font-medium">Agent notes</h3>
          <Markdown content={data.notes} />
        </section>
      ) : null}
      <section>
        <h3 className="mb-3 font-medium">Review body</h3>
        <Markdown content={data.content_markdown || "No description provided."} />
      </section>
      {!readOnly ? (
        <footer className="space-y-3 border-t border-border pt-4">
          {state ? (
            <p role="status" className="text-sm">
              {state.message}
            </p>
          ) : (
            <p className="text-sm text-fg-muted">Checking current status…</p>
          )}
          {error ? (
            <p role="alert" className="text-sm text-danger">
              {error}
            </p>
          ) : null}
          {!canCall ? <p role="alert">The review plugin route is unavailable.</p> : null}
          <div className="flex flex-wrap items-end gap-3">
            {data.actions.map((action) => (
              <div key={action.id} className="flex items-end gap-2">
                {action.options?.length ? (
                  <label className="text-xs text-fg-muted">
                    {action.label} method
                    <select
                      aria-label={`${action.label} method`}
                      className="mt-1 block rounded border border-border bg-bg-elevated px-3 py-2 text-sm text-fg"
                      value={options[action.id] || action.options[0].value}
                      disabled={busy}
                      onChange={(event) =>
                        setOptions({ ...options, [action.id]: event.target.value })
                      }
                    >
                      {action.options.map((option) => (
                        <option key={option.value} value={option.value}>
                          {option.label}
                        </option>
                      ))}
                    </select>
                  </label>
                ) : null}
                <Button
                  disabled={
                    busy || !canCall || !state || state.disabled_actions?.includes(action.id)
                  }
                  onClick={() =>
                    void act(action.id, options[action.id] || action.options?.[0]?.value)
                  }
                >
                  {action.label}
                </Button>
              </div>
            ))}
            <Button
              variant="outline"
              aria-label="Refresh review"
              disabled={busy || !canCall}
              onClick={() => void act()}
            >
              <RefreshCw size={16} />
              Refresh
            </Button>
            {state?.result ? (
              <Button variant="outline" disabled={busy} onClick={() => finish(state.result || {})}>
                Finish review
              </Button>
            ) : null}
            <Button variant="outline" disabled={busy} onClick={onCancel}>
              Dismiss
            </Button>
          </div>
        </footer>
      ) : null}
    </article>
  );
}
