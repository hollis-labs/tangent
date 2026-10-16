import { type FormEvent, useEffect, useRef, useState, useSyncExternalStore } from "react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  cancelOperatorElevation,
  getElevationPromptState,
  submitOperatorToken,
  subscribeElevationPrompt,
} from "@/lib/operator-elevation";

/**
 * The "Enter operator token" prompt (CW-20261016-0174). It opens when a
 * decision is refused for insufficient assurance; on success the refused
 * request is retried by `fetchWithElevation`.
 *
 * The token lives in this component's state only while typed, is cleared on
 * every submit and on dismiss, and is never persisted.
 */
export function OperatorTokenPrompt() {
  const prompt = useSyncExternalStore(subscribeElevationPrompt, getElevationPromptState);
  const [token, setToken] = useState("");
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (prompt.open) input.current?.focus();
    else setToken("");
  }, [prompt.open]);

  if (!prompt.open) return null;

  const submit = (event: FormEvent) => {
    event.preventDefault();
    const candidate = token;
    setToken("");
    void submitOperatorToken(candidate);
  };

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-bg/80 p-4">
      <form
        role="dialog"
        aria-modal="true"
        aria-labelledby="operator-token-title"
        className="w-full max-w-sm space-y-3 rounded-md border border-border bg-bg-elevated p-4 text-fg shadow"
        onSubmit={submit}
        onKeyDown={(event) => {
          if (event.key === "Escape") cancelOperatorElevation();
        }}
      >
        <h2 id="operator-token-title" className="text-sm font-medium">
          Enter operator token
        </h2>
        <p className="text-xs text-fg-muted">
          Deciding requires operator elevation. The token is sent once and not stored.
        </p>
        <Input
          ref={input}
          type="password"
          name="operator-token"
          autoComplete="off"
          aria-label="Operator token"
          value={token}
          disabled={prompt.submitting}
          onChange={(event) => setToken(event.target.value)}
        />
        {prompt.error ? (
          <p role="alert" className="text-xs text-danger">
            {prompt.error}
          </p>
        ) : null}
        <div className="flex justify-end gap-2">
          <Button
            type="button"
            variant="ghost"
            size="sm"
            onClick={() => cancelOperatorElevation()}
            disabled={prompt.submitting}
          >
            Cancel
          </Button>
          <Button type="submit" size="sm" disabled={prompt.submitting || !token}>
            {prompt.submitting ? "Checking…" : "Elevate"}
          </Button>
        </div>
      </form>
    </div>
  );
}
