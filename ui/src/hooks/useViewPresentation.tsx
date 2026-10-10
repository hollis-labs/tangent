import { Dialog } from "radix-ui";
import { type ReactNode, useRef, useState } from "react";
import { idCommand, navigateCommand, type UiHandler } from "@/lib/ui-channel";

/** Explicit targets, never a scrape of page content. The modal is read-only. */
export function useViewPresentation(
  rows: { id: string; title: string; body: ReactNode }[],
  navigate: (route: string) => void,
) {
  const [modalID, setModalID] = useState<string | null>(null);
  const focusTargets = useRef(new Map<string, HTMLButtonElement>());
  const restoreFocus = useRef<HTMLElement | null>(null);
  const modal = rows.find((row) => row.id === modalID);
  const open = (id: string) => {
    if (!rows.some((row) => row.id === id)) return "not_visible" as const;
    if (modal && modal.id !== id) return "not_visible" as const;
    if (modal) return "applied" as const;
    restoreFocus.current =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    setModalID(id);
    return "applied" as const;
  };
  const handlers: UiHandler[] = [
    navigateCommand(navigate),
    idCommand("open_modal", "ephemeral", (args) => open(args.id as string)),
    idCommand("close_modal", "ephemeral", (args) => {
      if (!modal || modal.id !== args.id) return "not_visible";
      setModalID(null);
      return "applied";
    }),
    idCommand("focus_item", "ephemeral", (args) => {
      const target = focusTargets.current.get(args.id as string);
      if (
        !target?.isConnected ||
        target.disabled ||
        target.closest("[inert], [aria-hidden=true]") ||
        target.getClientRects().length === 0 ||
        modal
      )
        return "not_visible";
      target.focus();
      return document.activeElement === target ? "applied" : "not_visible";
    }),
  ];
  return {
    handlers,
    open,
    modalID: modal?.id ?? null,
    targetRef: (id: string) => (element: HTMLButtonElement | null) => {
      if (element) focusTargets.current.set(id, element);
      else focusTargets.current.delete(id);
    },
    dialog: (
      <Dialog.Root
        open={Boolean(modal)}
        onOpenChange={(value) => {
          if (!value) setModalID(null);
        }}
      >
        <Dialog.Portal>
          <Dialog.Overlay className="fixed inset-0 z-40 bg-bg/80" />
          <Dialog.Content
            className="fixed inset-x-4 top-[10vh] z-50 mx-auto max-h-[80vh] max-w-2xl overflow-y-auto rounded border border-border bg-bg p-6 text-fg shadow-lg"
            onCloseAutoFocus={(event) => {
              event.preventDefault();
              const target = restoreFocus.current;
              if (target?.isConnected) target.focus();
            }}
          >
            <Dialog.Title className="text-lg font-semibold">{modal?.title}</Dialog.Title>
            <Dialog.Description className="mt-2 text-sm text-fg-muted">
              Read-only view details
            </Dialog.Description>
            <div className="my-4">{modal?.body}</div>
            <Dialog.Close asChild>
              <button type="button" className="rounded border border-border px-3 py-2">
                Close view details
              </button>
            </Dialog.Close>
          </Dialog.Content>
        </Dialog.Portal>
      </Dialog.Root>
    ),
  };
}
