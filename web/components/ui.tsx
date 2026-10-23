"use client";

import { forwardRef, useEffect, useState, type ButtonHTMLAttributes, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes } from "react";
import { ApiError } from "@/lib/api";
import { IconAlert, IconCheck, IconCopy, IconInbox, IconPlug, IconRefresh } from "./icons";

export function cx(...c: (string | false | null | undefined)[]): string {
  return c.filter(Boolean).join(" ");
}

// ---------------------------------------------------------------- buttons

type Variant = "primary" | "secondary" | "ghost" | "danger";

const variants: Record<Variant, string> = {
  primary:
    "bg-signal text-signal-ink hover:brightness-110 active:brightness-95 shadow-[inset_0_1px_0_rgb(255_255_255/0.25)] font-semibold",
  secondary: "bg-panel-2 text-fg border border-line-strong hover:bg-hover hover:border-[color:var(--faint)]",
  ghost: "text-fg-2 hover:text-fg hover:bg-hover",
  danger: "bg-err-dim text-err border border-[color:var(--err)]/30 hover:bg-[color:var(--err)]/20",
};

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: Variant;
  size?: "sm" | "md";
  icon?: ReactNode;
}

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", size = "md", icon, className, children, type = "button", ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={cx(
        "inline-flex select-none items-center justify-center gap-1.5 whitespace-nowrap rounded-lg transition-[background-color,border-color,color,filter,transform] duration-150 active:scale-[0.97] disabled:pointer-events-none disabled:opacity-45",
        size === "sm" ? "h-7 px-2.5 text-[12.5px]" : "h-8.5 px-3 text-[13px]",
        variants[variant],
        className,
      )}
      {...rest}
    >
      {icon}
      {children}
    </button>
  );
});

export function IconButton({
  label,
  className,
  children,
  ...rest
}: ButtonHTMLAttributes<HTMLButtonElement> & { label: string }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      className={cx(
        "inline-grid size-8 place-items-center rounded-lg text-muted transition-colors duration-150 hover:bg-hover hover:text-fg active:scale-95",
        className,
      )}
      {...rest}
    >
      {children}
    </button>
  );
}

// ------------------------------------------------------------------ inputs

const fieldBase =
  "h-8.5 w-full rounded-lg border border-line-strong bg-raised px-2.5 text-[13px] text-fg placeholder:text-faint transition-colors duration-150 hover:border-[color:var(--faint)] focus:border-[color:var(--signal)] focus:outline-none focus-visible:outline-none focus:ring-2 focus:ring-[color:var(--signal-dim)]";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input(
  { className, ...rest },
  ref,
) {
  return <input ref={ref} className={cx(fieldBase, className)} {...rest} />;
});

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <div className={cx("relative", className)}>
      <select className={cx(fieldBase, "cursor-pointer appearance-none pr-7")} {...rest}>
        {children}
      </select>
      <svg
        aria-hidden="true"
        className="pointer-events-none absolute right-2.5 top-1/2 -translate-y-1/2 text-muted"
        width="10"
        height="10"
        viewBox="0 0 10 10"
      >
        <path d="m2 3.5 3 3 3-3" fill="none" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      </svg>
    </div>
  );
}

export function Field({
  label,
  hint,
  htmlFor,
  children,
  className,
}: {
  label: string;
  hint?: ReactNode;
  htmlFor?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cx("flex flex-col gap-1.5", className)}>
      <label htmlFor={htmlFor} className="text-[12px] font-medium text-fg-2">
        {label}
      </label>
      {children}
      {hint ? <p className="text-[11.5px] leading-snug text-muted">{hint}</p> : null}
    </div>
  );
}

// -------------------------------------------------------- segmented control

export function Segmented<T extends string>({
  value,
  onChange,
  options,
  label,
  size = "md",
}: {
  value: T;
  onChange: (v: T) => void;
  options: readonly { id: T; label: string; title?: string }[];
  label: string;
  size?: "sm" | "md";
}) {
  return (
    <div
      role="radiogroup"
      aria-label={label}
      className="inline-flex rounded-lg border border-line-strong bg-raised p-0.5"
      onKeyDown={(e) => {
        const i = options.findIndex((o) => o.id === value);
        const d = e.key === "ArrowRight" || e.key === "ArrowDown" ? 1 : e.key === "ArrowLeft" || e.key === "ArrowUp" ? -1 : 0;
        if (!d) return;
        e.preventDefault();
        const next = options[(i + d + options.length) % options.length];
        if (next) {
          onChange(next.id);
          const btn = e.currentTarget.querySelector<HTMLButtonElement>(`[data-id="${next.id}"]`);
          btn?.focus();
        }
      }}
    >
      {options.map((o) => {
        const on = o.id === value;
        return (
          <button
            key={o.id}
            data-id={o.id}
            type="button"
            role="radio"
            aria-checked={on}
            tabIndex={on ? 0 : -1}
            title={o.title}
            onClick={() => onChange(o.id)}
            className={cx(
              "tnum rounded-md font-mono transition-[background-color,color,box-shadow] duration-150",
              size === "sm" ? "h-6 px-2 text-[11.5px]" : "h-7 px-2.5 text-[12px]",
              on ? "bg-panel-2 text-fg shadow-[0_0_0_1px_var(--line-strong),0_1px_2px_rgb(0_0_0/0.3)]" : "text-muted hover:text-fg",
            )}
          >
            {o.label}
          </button>
        );
      })}
    </div>
  );
}

// ------------------------------------------------------------------ panels

export function Panel({
  title,
  subtitle,
  actions,
  children,
  className,
  bodyClassName,
  style,
}: {
  title?: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
  bodyClassName?: string;
  style?: React.CSSProperties;
}) {
  return (
    <section
      style={style}
      className={cx(
        "rise flex min-w-0 flex-col rounded-xl border border-line bg-panel shadow-[inset_0_1px_0_rgb(255_255_255/0.025)]",
        className,
      )}
    >
      {title || actions ? (
        <header className="flex min-h-11 items-center justify-between gap-3 border-b border-line px-4 py-2.5">
          <div className="min-w-0">
            <h2 className="truncate text-[13px] font-semibold tracking-[-0.005em] text-fg">{title}</h2>
            {subtitle ? <p className="truncate text-[11.5px] text-muted">{subtitle}</p> : null}
          </div>
          {actions ? <div className="flex shrink-0 items-center gap-2">{actions}</div> : null}
        </header>
      ) : null}
      <div className={cx("min-w-0 flex-1", bodyClassName)}>{children}</div>
    </section>
  );
}

export function PageHeader({
  title,
  description,
  actions,
  meta,
}: {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  meta?: ReactNode;
}) {
  return (
    <div className="flex flex-col gap-4 pb-5 md:flex-row md:items-end md:justify-between">
      <div className="min-w-0">
        <h1 className="text-[22px] font-semibold leading-tight tracking-[-0.02em] text-fg">{title}</h1>
        {description ? <p className="mt-1 max-w-[62ch] text-[13px] text-muted">{description}</p> : null}
        {meta ? <div className="mt-2">{meta}</div> : null}
      </div>
      {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
    </div>
  );
}

// ------------------------------------------------------------------ badges

type Tone = "neutral" | "ok" | "warn" | "err" | "info" | "signal";

const tones: Record<Tone, string> = {
  neutral: "bg-panel-2 text-fg-2 border-line-strong",
  ok: "bg-ok-dim text-ok border-[color:var(--ok)]/25",
  warn: "bg-warn-dim text-warn border-[color:var(--warn)]/25",
  err: "bg-err-dim text-err border-[color:var(--err)]/25",
  info: "bg-info-dim text-info border-[color:var(--info)]/25",
  signal: "bg-signal-dim text-signal border-[color:var(--signal)]/25",
};

export function Badge({
  tone = "neutral",
  children,
  className,
  title,
}: {
  tone?: Tone;
  children: ReactNode;
  className?: string;
  title?: string;
}) {
  return (
    <span
      title={title}
      className={cx(
        "inline-flex h-5 items-center gap-1 whitespace-nowrap rounded-md border px-1.5 font-mono text-[11px] font-medium leading-none",
        tones[tone],
        className,
      )}
    >
      {children}
    </span>
  );
}

export function statusTone(status: number): Tone {
  if (status >= 500) return "err";
  if (status === 429 || status === 402) return "warn";
  if (status >= 400) return "err";
  return "ok";
}

export function StatusBadge({ status, code }: { status: number; code?: string }) {
  return (
    <Badge tone={statusTone(status)} title={code || undefined} className="tnum">
      {status}
    </Badge>
  );
}

/** A signal lamp: small glowing dot. */
export function Lamp({ tone, pulse, className }: { tone: Tone; pulse?: boolean; className?: string }) {
  const color: Record<Tone, string> = {
    neutral: "var(--faint)",
    ok: "var(--ok)",
    warn: "var(--warn)",
    err: "var(--err)",
    info: "var(--info)",
    signal: "var(--signal)",
  };
  return (
    <span
      aria-hidden="true"
      className={cx("inline-block size-2 shrink-0 rounded-full", pulse && "lamp-pulse", className)}
      style={{ background: color[tone], color: color[tone], boxShadow: `0 0 8px ${color[tone]}` }}
    />
  );
}

// ------------------------------------------------------------------ meters

export function Meter({ value, max, label }: { value: number; max: number; label: string }) {
  const ratio = max > 0 ? Math.min(1, value / max) : 0;
  const tone = ratio >= 0.9 ? "var(--err)" : ratio >= 0.7 ? "var(--warn)" : "var(--ok)";
  return (
    <div
      role="meter"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={max}
      aria-valuenow={value}
      className="h-1.5 w-full overflow-hidden rounded-full bg-panel-2 shadow-[inset_0_0_0_1px_var(--line)]"
    >
      <div
        className="h-full rounded-full transition-[width] duration-500 ease-out"
        style={{ width: `${Math.max(ratio > 0 ? 2 : 0, ratio * 100)}%`, background: tone }}
      />
    </div>
  );
}

// ------------------------------------------------------- skeleton & states

export function Skeleton({ className, style }: { className?: string; style?: React.CSSProperties }) {
  return <div aria-hidden="true" className={cx("skeleton", className)} style={style} />;
}

export function EmptyState({
  title,
  children,
  action,
  icon,
  compact,
}: {
  title: string;
  children?: ReactNode;
  action?: ReactNode;
  icon?: ReactNode;
  compact?: boolean;
}) {
  return (
    <div className={cx("flex flex-col items-center justify-center px-6 text-center", compact ? "py-8" : "py-14")}>
      <div className="mb-3 grid size-10 place-items-center rounded-xl border border-line-strong bg-panel-2 text-muted">
        {icon ?? <IconInbox size={18} />}
      </div>
      <p className="text-[13.5px] font-medium text-fg">{title}</p>
      {children ? <div className="mt-1 max-w-[46ch] text-[12.5px] text-muted">{children}</div> : null}
      {action ? <div className="mt-4">{action}</div> : null}
    </div>
  );
}

export function ErrorState({ error, onRetry, compact }: { error: unknown; onRetry?: () => void; compact?: boolean }) {
  const e = error instanceof ApiError ? error : null;
  if (e?.kind === "unconfigured") {
    return (
      <EmptyState
        compact={compact}
        icon={<IconPlug size={18} />}
        title="Backing store not configured"
        action={
          onRetry ? (
            <Button size="sm" icon={<IconRefresh size={13} />} onClick={onRetry}>
              Check again
            </Button>
          ) : null
        }
      >
        <span className="font-mono text-[11.5px] text-fg-2">{e.message}</span>
        <br />
        This view needs ClickHouse (analytics) or Postgres (keys). Configure it in <code className="font-mono">switchyard.yaml</code> and restart the
        gateway.
      </EmptyState>
    );
  }
  const msg = error instanceof Error ? error.message : "Something went wrong.";
  return (
    <div className={cx("flex flex-col items-center justify-center px-6 text-center", compact ? "py-8" : "py-14")} role="alert">
      <div className="mb-3 grid size-10 place-items-center rounded-xl border border-[color:var(--err)]/30 bg-err-dim text-err">
        <IconAlert size={18} />
      </div>
      <p className="text-[13.5px] font-medium text-fg">{e?.kind === "network" ? "Gateway unreachable" : "Couldn't load this data"}</p>
      <p className="mt-1 max-w-[52ch] font-mono text-[11.5px] text-muted">
        {e && e.status ? `${e.status} · ` : ""}
        {msg}
      </p>
      {onRetry ? (
        <Button className="mt-4" size="sm" icon={<IconRefresh size={13} />} onClick={onRetry}>
          Retry
        </Button>
      ) : null}
    </div>
  );
}

// ------------------------------------------------------------------- misc

export function CopyButton({ value, label = "Copy", className }: { value: string; label?: string; className?: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(false), 1600);
    return () => window.clearTimeout(t);
  }, [copied]);
  return (
    <Button
      size="sm"
      variant={copied ? "secondary" : "secondary"}
      className={className}
      aria-live="polite"
      icon={copied ? <IconCheck size={13} className="text-ok" /> : <IconCopy size={13} />}
      onClick={async () => {
        try {
          await navigator.clipboard.writeText(value);
        } catch {
          // Fallback for non-secure contexts.
          const ta = document.createElement("textarea");
          ta.value = value;
          document.body.appendChild(ta);
          ta.select();
          document.execCommand("copy");
          ta.remove();
        }
        setCopied(true);
      }}
    >
      {copied ? "Copied" : label}
    </Button>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return (
    <kbd className="inline-flex h-4.5 min-w-4.5 items-center justify-center rounded border border-line-strong bg-panel-2 px-1 font-mono text-[10px] text-muted">
      {children}
    </kbd>
  );
}
