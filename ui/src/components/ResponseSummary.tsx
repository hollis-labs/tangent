import { Markdown } from "@/components/markdown";

// Human-readable immutable response evidence, including structured decisions.
// Rendering it never reopens a form or submits another decision.
export function ResponseSummary({ value }: { value: unknown }) {
  if (value === null || value === undefined)
    return <p className="text-fg-muted">No response content.</p>;
  if (typeof value === "string") return <Markdown content={value} />;
  if (typeof value === "boolean") return <span>{value ? "Yes" : "No"}</span>;
  if (typeof value === "number") return <span>{value}</span>;
  if (Array.isArray(value))
    return (
      <ol className="space-y-3">
        {value.map((item) => (
          <li key={JSON.stringify(item)} className="rounded border border-border p-3">
            <ResponseSummary value={item} />
          </li>
        ))}
      </ol>
    );
  if (typeof value === "object")
    return (
      <dl className="space-y-3">
        {Object.entries(value)
          .filter(([, item]) => item !== undefined && item !== null && item !== "")
          .map(([key, item]) => (
            <div key={key}>
              <dt className="mb-1 text-xs font-medium capitalize text-fg-muted">
                {key.replaceAll("_", " ").replace(/([a-z])([A-Z])/g, "$1 $2")}
              </dt>
              <dd className="break-words">
                <ResponseSummary value={item} />
              </dd>
            </div>
          ))}
      </dl>
    );
  return null;
}
