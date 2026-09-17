import { PageShell } from "@/components/layout/PageShell";

export default function Index() {
  return (
    <PageShell
      as="main"
      className="mx-auto flex max-w-4xl items-center justify-center overflow-y-auto px-6 py-12"
    >
      <div className="space-y-4 text-center">
        <h1 className="text-3xl font-semibold tracking-tight">Tangent is waiting</h1>
        <p className="text-sm text-fg-muted">
          An agent will create a room and send an envelope when it needs input.
        </p>
        <p className="text-xs text-fg-faint">
          Open rooms appear in the tab strip above. You can switch between them without losing
          state.
        </p>
      </div>
    </PageShell>
  );
}
