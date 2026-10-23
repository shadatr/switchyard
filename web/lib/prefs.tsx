"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState } from "react";
import { type RangeId, isRangeId, refreshMsFor, resolveRange } from "./range";

interface Prefs {
  range: RangeId;
  setRange: (r: RangeId) => void;
  project: string; // project_id, "" = all
  setProject: (p: string) => void;
  /** Resolved window; moves forward on every refresh tick. */
  from: number;
  to: number;
  refresh: () => void;
  refreshMs: number;
}

const PrefsContext = createContext<Prefs | null>(null);
const RANGE_KEY = "switchyard.range";

export function PrefsProvider({ children }: { children: React.ReactNode }) {
  const [range, setRangeState] = useState<RangeId>("1h");
  const [project, setProject] = useState("");
  const [anchor, setAnchor] = useState(() => Date.now());

  useEffect(() => {
    try {
      const v = localStorage.getItem(RANGE_KEY);
      if (isRangeId(v)) setRangeState(v);
    } catch {
      // ignore
    }
  }, []);

  const setRange = useCallback((r: RangeId) => {
    setRangeState(r);
    setAnchor(Date.now());
    try {
      localStorage.setItem(RANGE_KEY, r);
    } catch {
      // ignore
    }
  }, []);

  const refreshMs = refreshMsFor(range);
  useEffect(() => {
    const id = window.setInterval(() => {
      if (document.visibilityState === "visible") setAnchor(Date.now());
    }, refreshMs);
    return () => window.clearInterval(id);
  }, [refreshMs]);

  const refresh = useCallback(() => setAnchor(Date.now()), []);
  const { from, to } = resolveRange(range, anchor);

  const value = useMemo<Prefs>(
    () => ({ range, setRange, project, setProject, from, to, refresh, refreshMs }),
    [range, setRange, project, from, to, refresh, refreshMs],
  );
  return <PrefsContext.Provider value={value}>{children}</PrefsContext.Provider>;
}

export function usePrefs(): Prefs {
  const p = useContext(PrefsContext);
  if (!p) throw new Error("usePrefs outside PrefsProvider");
  return p;
}
