import { PageShell } from "@/components/layout/PageShell";
import { PluginSettings } from "@/lib/plugin-settings/PluginSettings";

export default function Settings() {
  return (
    <PageShell as="main" className="overflow-y-auto">
      <header className="border-b border-border-subtle px-4 py-5 sm:px-6 lg:px-8">
        <h1 className="text-2xl font-semibold tracking-tight text-fg">Settings</h1>
        <p className="mt-1 max-w-xl text-sm leading-6 text-fg-muted">
          Plugin configuration and secret presence.
        </p>
      </header>
      <PluginSettings />
    </PageShell>
  );
}
