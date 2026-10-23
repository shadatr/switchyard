"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, useEffect, useState, type ReactNode } from "react";
import { SWRConfig } from "swr";
import { ApiError } from "@/lib/api";
import { useTargets } from "@/lib/hooks";
import { PrefsProvider } from "@/lib/prefs";
import { IS_DEMO, REPO_URL } from "@/lib/demo";
import { useSession } from "@/lib/session";
import { IconKey, IconLive, IconLogout, IconMoon, IconOverview, IconRequests, IconSun, IconTargets, Mark } from "./icons";
import { SignIn } from "./sign-in";
import { Lamp, cx } from "./ui";

const NAV = [
  { href: "/", label: "Overview", icon: IconOverview, key: "1" },
  { href: "/requests", label: "Requests", icon: IconRequests, key: "2" },
  { href: "/live", label: "Live", icon: IconLive, key: "3" },
  { href: "/keys", label: "Keys", icon: IconKey, key: "4" },
  { href: "/targets", label: "Targets", icon: IconTargets, key: "5" },
] as const;

export function Shell({ children }: { children: ReactNode }) {
  const { token, signOut } = useSession();

  // Any 401 anywhere means the token is no longer valid: back to sign-in.
  const onError = useCallback(
    (err: unknown) => {
      if (err instanceof ApiError && err.kind === "unauthorized") signOut("Your admin token was rejected. Sign in again.");
    },
    [signOut],
  );

  if (token === undefined) return <BootScreen />;
  if (!token) return <SignIn />;

  return (
    <SWRConfig
      value={{
        onError,
        shouldRetryOnError: (err) => !(err instanceof ApiError && (err.kind === "unauthorized" || err.kind === "unconfigured" || err.kind === "bad_request" || err.kind === "not_found")),
        errorRetryCount: 3,
      }}
    >
      <PrefsProvider>
        <Frame>{children}</Frame>
      </PrefsProvider>
    </SWRConfig>
  );
}

function BootScreen() {
  return (
    <div className="grid min-h-dvh place-items-center" aria-busy="true">
      <Mark size={28} className="animate-pulse opacity-70" />
    </div>
  );
}

function isActive(pathname: string, href: string) {
  return href === "/" ? pathname === "/" : pathname === href || pathname.startsWith(href + "/");
}

function Frame({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  const { signOut, baseUrl } = useSession();
  const targets = useTargets();
  const openBreakers = (targets.data ?? []).filter((t) => t.state !== "closed").length;
  const connected = !!targets.data && !targets.error;

  return (
    <div className="min-h-dvh md:grid md:grid-cols-[232px_minmax(0,1fr)]">
      <a
        href="#main"
        className="sr-only focus:not-sr-only focus:fixed focus:left-3 focus:top-3 focus:z-50 focus:rounded-md focus:bg-signal focus:px-3 focus:py-1.5 focus:text-signal-ink"
      >
        Skip to content
      </a>

      {/* Desktop sidebar */}
      <aside className="sticky top-0 hidden h-dvh flex-col border-r border-line bg-raised md:flex">
        <div className="flex h-14 items-center gap-2.5 px-4">
          <Mark size={22} />
          <span className="text-[15px] font-semibold tracking-[-0.02em]">Switchyard</span>
          <span className="ml-auto rounded border border-line-strong px-1 py-px font-mono text-[9.5px] uppercase tracking-[0.1em] text-faint">
            v0.1
          </span>
        </div>
        <nav aria-label="Primary" className="flex flex-col gap-0.5 px-2 pt-2">
          <p className="px-2.5 pb-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-faint">Gateway</p>
          {NAV.map((n) => {
            const on = isActive(pathname, n.href);
            const Icon = n.icon;
            return (
              <Link
                key={n.href}
                href={n.href}
                aria-current={on ? "page" : undefined}
                className={cx(
                  "group relative flex h-8 items-center gap-2.5 rounded-lg px-2.5 text-[13px] transition-colors duration-150",
                  on ? "bg-hover text-fg" : "text-fg-2 hover:bg-hover/60 hover:text-fg",
                )}
              >
                {on ? <span aria-hidden="true" className="absolute -left-2 top-1.5 bottom-1.5 w-[3px] rounded-r bg-signal" /> : null}
                <Icon size={15} className={on ? "text-signal" : "text-muted group-hover:text-fg-2"} />
                {n.label}
                {n.href === "/targets" && openBreakers > 0 ? (
                  <span className="tnum ml-auto rounded-md bg-err-dim px-1.5 font-mono text-[10.5px] text-err" title="Breakers not closed">
                    {openBreakers}
                  </span>
                ) : n.href === "/live" ? (
                  <Lamp tone="signal" pulse className="ml-auto size-1.5" />
                ) : (
                  <span className="ml-auto font-mono text-[10px] text-faint opacity-0 transition-opacity group-hover:opacity-100">g {n.key}</span>
                )}
              </Link>
            );
          })}
        </nav>
        <div className="mt-auto flex flex-col gap-2 p-3">
          <div className="rounded-xl border border-line bg-panel p-3">
            <div className="flex items-center gap-2 text-[12px]">
              <Lamp tone={IS_DEMO ? "signal" : connected ? "ok" : targets.error ? "err" : "neutral"} />
              <span className="text-fg-2">{IS_DEMO ? "Demo data" : connected ? "Connected" : targets.error ? "Unreachable" : "Connecting…"}</span>
            </div>
            <p className="mt-1 truncate font-mono text-[11px] text-muted" title={baseUrl}>
              {IS_DEMO ? "recorded from a live gateway" : baseUrl.replace(/^https?:\/\//, "")}
            </p>
          </div>
          <div className="flex items-center gap-1">
            <ThemeToggle />
            {IS_DEMO ? null : (
              <button
                type="button"
                onClick={() => signOut()}
                className="ml-auto inline-flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-[12.5px] text-muted transition-colors hover:bg-hover hover:text-fg"
              >
                <IconLogout size={14} />
                Sign out
              </button>
            )}
          </div>
        </div>
      </aside>

      {/* Mobile top bar */}
      <header className="sticky top-0 z-30 border-b border-line bg-raised/90 backdrop-blur-md md:hidden">
        <div className="flex h-12 items-center gap-2 px-4">
          <Mark size={20} />
          <span className="text-[14px] font-semibold tracking-[-0.02em]">Switchyard</span>
          <Lamp tone={connected ? "ok" : targets.error ? "err" : "neutral"} className="ml-1" />
          <div className="ml-auto flex items-center">
            <ThemeToggle />
            {IS_DEMO ? null : (
              <button
                type="button"
                aria-label="Sign out"
                onClick={() => signOut()}
                className="grid size-8 place-items-center rounded-lg text-muted hover:bg-hover hover:text-fg"
              >
                <IconLogout size={15} />
              </button>
            )}
          </div>
        </div>
        <nav aria-label="Primary" className="-mb-px flex gap-1 overflow-x-auto px-3">
          {NAV.map((n) => {
            const on = isActive(pathname, n.href);
            return (
              <Link
                key={n.href}
                href={n.href}
                aria-current={on ? "page" : undefined}
                className={cx(
                  "shrink-0 border-b-2 px-2.5 pb-2 pt-1 text-[13px] transition-colors",
                  on ? "border-[color:var(--signal)] text-fg" : "border-transparent text-muted hover:text-fg",
                )}
              >
                {n.label}
              </Link>
            );
          })}
        </nav>
      </header>

      <main id="main" className="relative min-w-0">
        {IS_DEMO ? <DemoBanner /> : null}
        <div className="yard-grid pointer-events-none absolute inset-x-0 top-0 h-[280px] opacity-60" />
        <div className="relative mx-auto w-full max-w-[1400px] px-4 py-5 md:px-8 md:py-7">{children}</div>
      </main>
      <GoShortcuts />
    </div>
  );
}

function DemoBanner() {
  return (
    <div className="relative z-10 border-b border-line bg-raised/80 px-4 py-2 text-[12.5px] text-fg-2 md:px-8">
      <span className="mr-2 rounded border border-[color:var(--signal)] px-1.5 py-px font-mono text-[10px] uppercase tracking-[0.12em] text-signal">
        Demo
      </span>
      Recorded from a real Switchyard gateway routing to simulated LLM upstreams. The live tail replays that traffic; key changes stay in your browser.
      {REPO_URL ? (
        <>
          {" "}
          <a href={REPO_URL} className="text-fg underline decoration-line-strong underline-offset-2 hover:decoration-[color:var(--signal)]">
            Run the full stack with Docker →
          </a>
        </>
      ) : null}
    </div>
  );
}

/** "g" then 1–5 jumps between pages (Linear-style). */
function GoShortcuts() {
  const pathname = usePathname();
  useEffect(() => {
    let armed = 0;
    const onKey = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null;
      if (e.metaKey || e.ctrlKey || e.altKey || el?.closest("input, textarea, select, [contenteditable=true], dialog[open]")) return;
      if (e.key === "g") {
        armed = Date.now();
        return;
      }
      if (Date.now() - armed < 1200) {
        const n = NAV.find((x) => x.key === e.key);
        if (n && n.href !== pathname) {
          // Client-side navigation via a synthetic link click keeps Next's router.
          document.querySelector<HTMLAnchorElement>(`aside a[href="${n.href}"], header a[href="${n.href}"]`)?.click();
        }
        armed = 0;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pathname]);
  return null;
}

function ThemeToggle() {
  const [theme, setTheme] = useState<"dark" | "light">("dark");
  useEffect(() => {
    setTheme(document.documentElement.dataset.theme === "light" ? "light" : "dark");
  }, []);
  const next = theme === "dark" ? "light" : "dark";
  return (
    <button
      type="button"
      aria-label={`Switch to ${next} theme`}
      title={`Switch to ${next} theme`}
      onClick={() => {
        document.documentElement.dataset.theme = next;
        try {
          localStorage.setItem("switchyard.theme", next);
        } catch {
          // ignore
        }
        setTheme(next);
      }}
      className="grid size-8 place-items-center rounded-lg text-muted transition-colors hover:bg-hover hover:text-fg"
    >
      {theme === "dark" ? <IconSun size={15} /> : <IconMoon size={15} />}
    </button>
  );
}
