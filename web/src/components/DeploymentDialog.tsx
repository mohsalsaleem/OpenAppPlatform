import { useEffect, useRef, type ReactNode } from "react";

/** Native dialog supplies focus containment, Escape handling, and inert background. */
export function DeploymentDialog({
  onClose,
  children,
}: {
  onClose: () => void;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const dialog = ref.current!;
    dialog.showModal();
    return () => {
      if (dialog.open) dialog.close();
    };
  }, []);
  return (
    <dialog
      ref={ref}
      className="modal"
      aria-labelledby="deploy-title"
      onCancel={onClose}
    >
      {children}
    </dialog>
  );
}
