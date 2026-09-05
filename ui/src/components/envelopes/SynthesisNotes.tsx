import { Button } from "@/components/ui/button";
import { Card, CardContent, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";

export interface SynthesisOutlineItem {
  label?: string;
  description?: string;
}

export interface SynthesisOutline {
  title?: string;
  items?: SynthesisOutlineItem[];
}

export interface SynthesisNotesEnvelope {
  v: number;
  id: string;
  type: "tangent.synthesis-notes";
  typeVersion?: string;
  title?: string;
  context?: string;
  presentation?: "inline" | "modal" | "drawer" | "sidecar" | "fullscreen";
  data?: {
    visibility?: "hidden" | "visible";
    has_private_notes?: boolean;
    summary?: string;
    outline_state?: "absent" | "present" | "skipped";
    outline?: SynthesisOutline;
  };
  meta?: Record<string, unknown>;
}

export interface SynthesisNotesResponse {
  v: 1;
  envelopeId: string;
  kind: "ack";
  status: "submitted";
  completedAt: string;
}

export type SynthesisNotesProps = {
  envelope: SynthesisNotesEnvelope;
  onSubmit: (response: SynthesisNotesResponse) => void;
  onCancel: () => void;
};

export function SynthesisNotes({ envelope, onSubmit, onCancel }: SynthesisNotesProps) {
  // No terminal gate by design: this workflow is a read-only acknowledgement
  // with no editable controls, so "Continue" is never blocked and there is
  // nothing for a submit gate to name.
  const visibility = envelope.data?.visibility ?? "hidden";
  const outlineState = envelope.data?.outline_state ?? "absent";
  const outline = envelope.data?.outline;
  const summary = envelope.data?.summary;

  const handleContinue = () => {
    onSubmit({
      v: 1,
      envelopeId: envelope.id,
      kind: "ack",
      status: "submitted",
      completedAt: new Date().toISOString(),
    });
  };

  return (
    <Card data-testid="synthesis-notes-root" className="w-full max-w-3xl">
      <CardHeader className="space-y-3">
        <div className="space-y-1">
          <CardTitle className="text-lg">{envelope.title ?? "Synthesis notes"}</CardTitle>
          {envelope.context ? (
            <p className="whitespace-pre-wrap text-sm text-zinc-400">{envelope.context}</p>
          ) : null}
        </div>
      </CardHeader>

      {/*
        The card swaps whole content branches — hidden, outline, skipped, empty
        — depending on envelope state, and did so inside plain divs. A screen
        reader on a room that flips from hidden to visible heard nothing at all.
      */}
      <CardContent className="space-y-4" aria-live="polite" data-testid="synthesis-notes-content">
        {visibility === "hidden" ? (
          <section
            className="rounded-lg border border-dashed border-zinc-700 bg-zinc-950/60 p-4"
            data-testid="synthesis-notes-hidden"
          >
            <p className="text-sm text-zinc-100">Private synthesis notes are saved on this room.</p>
            <p className="mt-2 text-sm text-zinc-400">
              The user-facing outline preview stays hidden until the workflow reaches drafting.
            </p>
          </section>
        ) : (
          <section className="space-y-4" data-testid="synthesis-notes-visible">
            {summary ? (
              <div className="space-y-2">
                <h3 className="text-xs font-medium uppercase tracking-wide text-zinc-500">
                  Summary
                </h3>
                <p className="whitespace-pre-wrap text-sm leading-6 text-zinc-100">{summary}</p>
              </div>
            ) : null}

            {outlineState === "present" && outline ? (
              <div className="space-y-3" data-testid="synthesis-notes-outline">
                <h3 className="text-xs font-medium uppercase tracking-wide text-zinc-500">
                  Outline preview
                </h3>
                {outline.title ? (
                  <p className="text-sm font-medium text-zinc-100">{outline.title}</p>
                ) : null}
                <div className="space-y-3">
                  {(outline.items ?? []).map((item) => (
                    <div
                      key={`${item.label ?? "item"}-${item.description ?? ""}`}
                      className="rounded-md border border-zinc-800 bg-zinc-950 p-3"
                    >
                      {item.label ? (
                        <p className="text-sm font-medium text-zinc-100">{item.label}</p>
                      ) : null}
                      {item.description ? (
                        <p className="mt-1 whitespace-pre-wrap text-sm text-zinc-400">
                          {item.description}
                        </p>
                      ) : null}
                    </div>
                  ))}
                </div>
              </div>
            ) : null}

            {outlineState === "skipped" ? (
              <div
                className="rounded-md border border-zinc-800 bg-zinc-950 p-3"
                data-testid="synthesis-notes-skipped"
              >
                <p className="text-sm text-zinc-300">
                  Outline preview was explicitly skipped for this draft.
                </p>
              </div>
            ) : null}

            {outlineState === "absent" && !summary ? (
              <div
                className="rounded-md border border-zinc-800 bg-zinc-950 p-3"
                data-testid="synthesis-notes-empty"
              >
                <p className="text-sm text-zinc-300">
                  Synthesis is ready. No outline preview is attached.
                </p>
              </div>
            ) : null}
          </section>
        )}
      </CardContent>

      <CardFooter className="justify-end gap-3">
        <Button
          type="button"
          variant="ghost"
          onClick={onCancel}
          data-testid="synthesis-notes-cancel"
        >
          Cancel
        </Button>
        <Button type="button" onClick={handleContinue} data-testid="synthesis-notes-submit">
          Continue
        </Button>
      </CardFooter>
    </Card>
  );
}
