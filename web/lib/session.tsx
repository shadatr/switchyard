"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { ApiError, SwitchyardClient, baseUrl } from "./api";
import { DEMO_TOKEN, IS_DEMO, demoFetch } from "./demo";

const STORAGE_KEY = "switchyard.admin_token";

interface Session {
  /** undefined while hydrating from sessionStorage. */
  token: string | null | undefined;
  client: SwitchyardClient | null;
  baseUrl: string;
  notice: string | null;
  signIn: (token: string) => Promise<void>;
  signOut: (notice?: string) => void;
}

const SessionContext = createContext<Session | null>(null);

function readToken(): string | null {
  try {
    return sessionStorage.getItem(STORAGE_KEY);
  } catch {
    return null;
  }
}

function writeToken(token: string | null) {
  try {
    if (token) sessionStorage.setItem(STORAGE_KEY, token);
    else sessionStorage.removeItem(STORAGE_KEY);
  } catch {
    // storage unavailable (private mode): the session lives in memory only
  }
}

export function SessionProvider({ children }: { children: React.ReactNode }) {
  const [token, setToken] = useState<string | null | undefined>(undefined);
  const [notice, setNotice] = useState<string | null>(null);
  const url = IS_DEMO ? "demo" : baseUrl();

  // sessionStorage only exists in the browser: read it after hydration.
  // Demo mode needs no sign-in.
  useEffect(() => {
    setToken(IS_DEMO ? DEMO_TOKEN : readToken());
  }, []);

  const client = useMemo(
    () => (token ? new SwitchyardClient({ baseUrl: url, token, fetch: IS_DEMO ? demoFetch : undefined }) : null),
    [token, url],
  );

  const signIn = useCallback(
    async (candidate: string) => {
      const t = candidate.trim();
      if (!t) throw new ApiError(401, "Enter the admin token.");
      // Validate against a cheap endpoint that needs neither ClickHouse nor Postgres.
      await new SwitchyardClient({ baseUrl: url, token: t }).targets();
      writeToken(t);
      setNotice(null);
      setToken(t);
    },
    [url],
  );

  const signOut = useCallback((msg?: string) => {
    if (IS_DEMO) return; // nothing to sign out of
    writeToken(null);
    setNotice(msg ?? null);
    setToken(null);
  }, []);

  const value = useMemo<Session>(
    () => ({ token, client, baseUrl: url, notice, signIn, signOut }),
    [token, client, url, notice, signIn, signOut],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession(): Session {
  const s = useContext(SessionContext);
  if (!s) throw new Error("useSession outside SessionProvider");
  return s;
}

/** The client for pages rendered behind the sign-in gate. */
export function useClient(): SwitchyardClient {
  const { client } = useSession();
  if (!client) throw new Error("useClient called without a session");
  return client;
}
