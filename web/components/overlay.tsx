"use client";

import { useEffect, useRef, type ReactNode } from "react";
import { IconX } from "./icons";
import { IconButton, cx } from "./ui";

/**
 * Native <dialog> gives us a focus trap, Escape-to-close, inert background
 * and top-layer stacking for free.
 */
function useDialog(open: boolean, onClose: () => void) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    if (open && !d.open) d.showModal();
    if (!open && d.open) d.close();
  }, [open]);
  useEffect(() => {
    const d = ref.current;
    if (!d) return;
    const onCancel = (e: Event) => {
      e.preventDefault();
      onClose();
    };
    d.addEventListener("cancel", onCancel);
    return () => d.removeEventListener("cancel", onCancel);
  }, [onClose]);
  return ref;
}

export function Sheet({
  open,
  onClose,
  title,
  subtitle,
  children,
  footer,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  subtitle?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  const ref = useDialog(open, onClose);
  return (
    <dialog
      ref={ref}
      aria-label={typeof title === "string" ? title : undefined}
      className="sheet fixed inset-y-0 right-0 left-auto m-0 h-dvh max-h-dvh w-full max-w-[480px] border-l border-line-strong bg-raised p-0 text-fg shadow-pop"
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      {open ? (
        <div className="flex h-full flex-col">
          <header className="flex items-start justify-between gap-3 border-b border-line px-5 py-4">
            <div className="min-w-0">
              <div className="text-[15px] font-semibold tracking-[-0.01em]">{title}</div>
              {subtitle ? <div className="mt-0.5 truncate text-[12px] text-muted">{subtitle}</div> : null}
            </div>
            <IconButton label="Close" onClick={onClose} className="-mr-1.5 -mt-1">
              <IconX />
            </IconButton>
          </header>
          <div className="flex-1 overflow-y-auto px-5 py-4">{children}</div>
          {footer ? <footer className="border-t border-line px-5 py-3">{footer}</footer> : null}
        </div>
      ) : null}
    </dialog>
  );
}

export function Modal({
  open,
  onClose,
  title,
  description,
  children,
  footer,
  className,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  className?: string;
}) {
  const ref = useDialog(open, onClose);
  return (
    <dialog
      ref={ref}
      aria-label={typeof title === "string" ? title : undefined}
      className={cx(
        "modal m-auto w-[calc(100%-32px)] max-w-[520px] rounded-2xl border border-line-strong bg-raised p-0 text-fg shadow-pop",
        className,
      )}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      {open ? (
        <div className="flex max-h-[85dvh] flex-col">
          <header className="flex items-start justify-between gap-3 px-5 pb-2 pt-5">
            <div>
              <h2 className="text-[16px] font-semibold tracking-[-0.01em]">{title}</h2>
              {description ? <p className="mt-1 text-[12.5px] text-muted">{description}</p> : null}
            </div>
            <IconButton label="Close" onClick={onClose} className="-mr-1.5 -mt-1">
              <IconX />
            </IconButton>
          </header>
          <div className="overflow-y-auto px-5 py-3">{children}</div>
          {footer ? <footer className="flex items-center justify-end gap-2 border-t border-line px-5 py-3">{footer}</footer> : null}
        </div>
      ) : null}
    </dialog>
  );
}
