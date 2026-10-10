import { Children, isValidElement, useRef, type ReactNode } from "react";
import { Dialog, DialogContent, DialogTitle } from "@/components/ui/dialog";

/** All operation reviews share the shadcn/Radix focus, overlay and Escape behavior. */
export function DeploymentDialog({
  onClose,
  children,
}: {
  onClose: () => void;
  children: ReactNode;
}) {
  const previousFocus = useRef(document.activeElement as HTMLElement | null);
  const content = Children.toArray(children).map((child) =>
    isValidElement<{ children?: ReactNode; id?: string }>(child) &&
    child.type === "h2" ? (
      <DialogTitle key="title">{child.props.children}</DialogTitle>
    ) : (
      child
    ),
  );
  return (
    <Dialog
      open
      onOpenChange={(open) => {
        if (!open) onClose();
      }}
    >
      <DialogContent
        className="sm:max-w-2xl"
        showCloseButton={false}
        aria-describedby={undefined}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          const original = previousFocus.current;
          if (original?.isConnected && original.getClientRects().length)
            original.focus();
          else
            document
              .querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')
              ?.focus();
        }}
        onInteractOutside={(event) => event.preventDefault()}
      >
        {content}
      </DialogContent>
    </Dialog>
  );
}
