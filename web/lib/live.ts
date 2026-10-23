"use client";

import { useEffect, useRef, useState } from "react";
import { type LiveEvent, backoffMs, liveUrl, parseLiveMessage } from "./api";
import { useSession } from "./session";

export const LIVE_CAP = 500;
const RATE_WINDOW_MS = 5000;

export type LiveStatus = "connecting" | "open" | "reconnecting" | "unauthorized";

export interface LiveRow extends LiveEvent {
  /** Monotonic client-side sequence, stable React key even if ids repeat. */
  seq: number;
}

export interface LiveState {
  rows: LiveRow[];
  status: LiveStatus;
  rate: number; // events/sec over the last 5s
  lastSkipped: number;
  skippedBatches: number;
  received: number;
  buffered: number; // events held back while paused
  retryInMs: number | null;
}

/** Prepends newest-first, capping the list. Exported for tests. */
export function mergeNewest<T>(current: T[], incomingOldestFirst: T[], cap = LIVE_CAP): T[] {
  if (incomingOldestFirst.length === 0) return current;
  const fresh = incomingOldestFirst.slice(-cap).reverse();
  return fresh.concat(current).slice(0, cap);
}

export function useLiveTail(paused: boolean): LiveState {
  const { token, baseUrl, signOut } = useSession();
  const [state, setState] = useState<LiveState>({
    rows: [],
    status: "connecting",
    rate: 0,
    lastSkipped: 0,
    skippedBatches: 0,
    received: 0,
    buffered: 0,
    retryInMs: null,
  });
  const pausedRef = useRef(paused);
  const pending = useRef<LiveRow[]>([]);
  const arrivals = useRef<{ at: number; n: number }[]>([]);
  const seq = useRef(0);

  useEffect(() => {
    pausedRef.current = paused;
    if (!paused && pending.current.length > 0) {
      const flush = pending.current;
      pending.current = [];
      setState((s) => ({ ...s, rows: mergeNewest(s.rows, flush), buffered: 0 }));
    }
  }, [paused]);

  useEffect(() => {
    if (!token) return;
    let ws: WebSocket | null = null;
    let attempt = 0;
    let retryTimer: number | undefined;
    let disposed = false;
    let openedAt = 0;

    const connect = () => {
      setState((s) => ({ ...s, status: attempt === 0 ? "connecting" : "reconnecting", retryInMs: null }));
      ws = new WebSocket(liveUrl(baseUrl, token));
      ws.onopen = () => {
        openedAt = Date.now();
        setState((s) => ({ ...s, status: "open", retryInMs: null }));
      };
      ws.onmessage = (ev) => {
        const msg = parseLiveMessage(ev.data);
        if (!msg) return;
        attempt = 0;
        const now = Date.now();
        arrivals.current.push({ at: now, n: msg.events.length });
        const rows = msg.events.map((e) => ({ ...e, seq: ++seq.current }));
        if (pausedRef.current) {
          pending.current = pending.current.concat(rows).slice(-LIVE_CAP);
        }
        setState((s) => ({
          ...s,
          rows: pausedRef.current ? s.rows : mergeNewest(s.rows, rows),
          buffered: pausedRef.current ? pending.current.length : 0,
          received: s.received + rows.length,
          lastSkipped: msg.skipped,
          skippedBatches: s.skippedBatches + (msg.skipped > 0 ? 1 : 0),
        }));
      };
      ws.onclose = (ev) => {
        if (disposed) return;
        // The server rejects bad tokens before the upgrade, which browsers
        // surface as an abnormal close (1006) without ever opening.
        if (openedAt === 0 && attempt >= 3 && ev.code === 1006) {
          // Re-check the token over REST to tell auth failures from outages.
          fetch(`${baseUrl}/admin/api/targets`, { headers: { Authorization: `Bearer ${token}` } })
            .then((r) => {
              if (r.status === 401) {
                setState((s) => ({ ...s, status: "unauthorized" }));
                signOut("Your admin token was rejected. Sign in again.");
              }
            })
            .catch(() => {});
        }
        openedAt = 0;
        const wait = backoffMs(attempt++);
        setState((s) => ({ ...s, status: "reconnecting", retryInMs: wait }));
        retryTimer = window.setTimeout(connect, wait);
      };
    };
    connect();
    return () => {
      disposed = true;
      window.clearTimeout(retryTimer);
      ws?.close();
    };
  }, [token, baseUrl, signOut]);

  // Recompute the rate on a steady tick so it decays when traffic stops.
  useEffect(() => {
    const id = window.setInterval(() => {
      const cutoff = Date.now() - RATE_WINDOW_MS;
      arrivals.current = arrivals.current.filter((a) => a.at >= cutoff);
      const n = arrivals.current.reduce((sum, a) => sum + a.n, 0);
      setState((s) => {
        const rate = n / (RATE_WINDOW_MS / 1000);
        return s.rate === rate ? s : { ...s, rate };
      });
    }, 500);
    return () => window.clearInterval(id);
  }, []);

  return state;
}
