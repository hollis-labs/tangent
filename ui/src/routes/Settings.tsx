import { PageShell } from "@/components/layout/PageShell";
import { ThemeToggle } from "@/components/ThemeToggle";
import { PluginLifecycle } from "@/lib/plugin-settings/PluginLifecycle";
import { PluginSettings } from "@/lib/plugin-settings/PluginSettings";

export default function Settings() {
  return (
    <PageShell className="overflow-y-auto">
      <header className="border-b border-border-subtle px-4 py-5 sm:px-6 lg:px-8">
        <div className="flex items-start justify-between gap-4">
          <h2 className="text-2xl font-semibold tracking-tight text-fg">Settings</h2>
          <ThemeToggle />
        </div>
        <p className="mt-1 max-w-xl text-sm leading-6 text-fg-muted">
          Plugin enable intent, runtime state, configuration and secret presence.
        </p>
      </header>
      <PluginLifecycle />
      <PluginSettings />
    </PageShell>
  );
}
