export default function Index() {
  return (
    <main className="mx-auto flex min-h-[calc(100vh-64px)] max-w-4xl items-center justify-center px-6 py-12 text-zinc-100">
      <div className="space-y-4 text-center">
        <h1 className="text-3xl font-semibold tracking-tight">Tangent is waiting</h1>
        <p className="text-sm text-zinc-400">
          An agent will create a room and send an envelope when it needs input.
        </p>
        <p className="text-xs text-zinc-500">
          Open rooms appear in the tab strip above. You can switch between them without losing
          state.
        </p>
      </div>
    </main>
  );
}
