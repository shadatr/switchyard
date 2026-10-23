"use client";

import { useState } from "react";
import { ApiError } from "@/lib/api";
import { useSession } from "@/lib/session";
import { IconLock, Mark } from "./icons";
import { Button, Input } from "./ui";

export function SignIn() {
  const { signIn, baseUrl, notice } = useSession();
  const [token, setToken] = useState("");
  const [reveal, setReveal] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError(null);
    try {
      await signIn(token);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(
          err.kind === "unauthorized"
            ? "That token was rejected by the gateway."
            : err.kind === "network"
              ? `Can't reach ${baseUrl}. Is the gateway running, and is this origin in admin.cors_origins?`
              : err.message,
        );
      } else {
        setError("Sign-in failed.");
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <main className="relative grid min-h-dvh place-items-center overflow-hidden px-4 py-10">
      <div className="yard-grid pointer-events-none absolute inset-0" />
      <div
        aria-hidden="true"
        className="pointer-events-none absolute -top-40 left-1/2 h-[420px] w-[720px] -translate-x-1/2 rounded-full opacity-[0.10] blur-3xl"
        style={{ background: "radial-gradient(closest-side, var(--signal), transparent)" }}
      />
      <div className="rise relative w-full max-w-[400px]">
        <div className="mb-8 flex items-center gap-2.5">
          <Mark size={28} />
          <span className="text-[17px] font-semibold tracking-[-0.02em]">Switchyard</span>
          <span className="rounded-md border border-line-strong px-1.5 py-0.5 font-mono text-[10.5px] uppercase tracking-[0.08em] text-muted">
            admin
          </span>
        </div>
        <h1 className="text-[30px] leading-[1.1] tracking-[-0.025em]">
          <span className="font-semibold">Every request,</span>{" "}
          <span className="font-serif text-[34px] italic text-signal">routed.</span>
        </h1>
        <p className="mt-3 text-[13.5px] text-muted">
          Enter the gateway&apos;s admin token to open the dashboard. It&apos;s kept in this tab&apos;s session storage and cleared when you sign out
          or close the tab.
        </p>

        <form onSubmit={submit} className="mt-7 rounded-2xl border border-line-strong bg-panel p-5 shadow-pop">
          <label htmlFor="token" className="text-[12px] font-medium text-fg-2">
            Admin token
          </label>
          <div className="relative mt-1.5">
            <Input
              id="token"
              name="token"
              type={reveal ? "text" : "password"}
              autoComplete="off"
              spellCheck={false}
              autoFocus
              placeholder="SWITCHYARD_ADMIN_TOKEN"
              value={token}
              onChange={(e) => setToken(e.target.value)}
              className="h-10 pr-16 font-mono"
              aria-invalid={error ? true : undefined}
              aria-describedby={error ? "token-error" : undefined}
            />
            <button
              type="button"
              onClick={() => setReveal((v) => !v)}
              className="absolute right-1.5 top-1/2 h-7 -translate-y-1/2 rounded-md px-2 text-[11.5px] text-muted hover:bg-hover hover:text-fg"
            >
              {reveal ? "Hide" : "Show"}
            </button>
          </div>
          {error || notice ? (
            <p id="token-error" role="alert" className="mt-2 text-[12px] text-err">
              {error ?? notice}
            </p>
          ) : null}
          <Button type="submit" variant="primary" className="mt-4 h-10 w-full" disabled={busy || !token.trim()} icon={<IconLock size={14} />}>
            {busy ? "Checking…" : "Sign in"}
          </Button>
          <div className="mt-4 flex items-center justify-between gap-3 border-t border-line pt-3 text-[11.5px]">
            <span className="text-muted">Gateway</span>
            <span className="truncate font-mono text-fg-2">{baseUrl}</span>
          </div>
        </form>
      </div>
    </main>
  );
}
