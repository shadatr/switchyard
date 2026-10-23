"use client";

import { useMemo } from "react";
import { useBreakdown, useKeys } from "@/lib/hooks";
import { usePrefs } from "@/lib/prefs";
import { RANGE_PRESETS } from "@/lib/range";
import { IconRefresh } from "./icons";
import { IconButton, Segmented, Select, cx } from "./ui";

export function RangePicker() {
  const { range, setRange } = usePrefs();
  return (
    <Segmented
      label="Time range"
      value={range}
      onChange={setRange}
      options={RANGE_PRESETS.map((p) => ({ id: p.id, label: p.label, title: p.long }))}
    />
  );
}

/** Projects known to the key store, as {id, name}. */
export function useProjects() {
  const keys = useKeys();
  const projects = useMemo(() => {
    const m = new Map<string, string>();
    for (const k of keys.data ?? []) m.set(k.project_id, k.project);
    return [...m.entries()].map(([id, name]) => ({ id, name })).sort((a, b) => a.name.localeCompare(b.name));
  }, [keys.data]);
  return { projects, error: keys.error, isLoading: keys.isLoading };
}

export function ProjectPicker({ className }: { className?: string }) {
  const { project, setProject } = usePrefs();
  const known = useProjects();
  const seen = useBreakdown("project");
  // Union of projects in the key store and project ids actually present in
  // the logs for this window (labelled by name when the key store knows it).
  const projects = useMemo(() => {
    const names = new Map(known.projects.map((p) => [p.id, p.name]));
    const ids = new Set<string>(known.projects.map((p) => p.id));
    for (const g of seen.data?.groups ?? []) if (g.key) ids.add(g.key);
    if (project) ids.add(project);
    return [...ids].map((id) => ({ id, name: names.get(id) ?? id })).sort((a, b) => a.name.localeCompare(b.name));
  }, [known.projects, seen.data, project]);
  const error = known.error && seen.error;
  return (
    <Select
      aria-label="Project"
      value={project}
      onChange={(e) => setProject(e.target.value)}
      className={cx(className ?? "w-[160px]")}
      disabled={!!error && projects.length === 0}
      title={error ? "Project list unavailable (key store not reachable)" : undefined}
    >
      <option value="">All projects</option>
      {projects.map((p) => (
        <option key={p.id} value={p.id}>
          {p.name}
        </option>
      ))}
    </Select>
  );
}

export function RefreshButton({ onClick, spinning }: { onClick: () => void; spinning?: boolean }) {
  return (
    <IconButton label="Refresh" onClick={onClick} className="border border-line-strong bg-raised">
      <IconRefresh size={14} className={spinning ? "animate-spin [animation-duration:900ms]" : undefined} />
    </IconButton>
  );
}
