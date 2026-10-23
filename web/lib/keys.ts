// Pure helpers for the key create/edit forms (unit-tested).

import type { ApiKey, KeyCreateInput, KeyUpdateInput } from "./api";

export interface KeyForm {
  name: string;
  project: string;
  allowedModels: string; // comma/space/newline separated; empty = all models
  rpm: string; // empty = unlimited
  tpm: string;
  budget: string; // USD per month; empty = no budget
}

export type FormErrors = Partial<Record<keyof KeyForm, string>>;

export function emptyForm(): KeyForm {
  return { name: "", project: "default", allowedModels: "", rpm: "", tpm: "", budget: "" };
}

export function formFromKey(k: ApiKey): KeyForm {
  return {
    name: k.name,
    project: k.project,
    allowedModels: k.allowed_models.join(", "),
    rpm: k.rpm_limit === null ? "" : String(k.rpm_limit),
    tpm: k.tpm_limit === null ? "" : String(k.tpm_limit),
    budget: k.monthly_budget_usd === null ? "" : String(k.monthly_budget_usd),
  };
}

export function parseModels(s: string): string[] {
  const out: string[] = [];
  for (const m of s.split(/[\s,]+/)) {
    const t = m.trim();
    if (t && !out.includes(t)) out.push(t);
  }
  return out;
}

/** "" → null; otherwise a positive integer or NaN. Accepts "1,000" and "1_000". */
export function parseLimit(s: string): number | null {
  const t = s.trim().replace(/[,_\s]/g, "");
  if (t === "") return null;
  if (!/^\d+$/.test(t)) return Number.NaN;
  const n = Number(t);
  return n > 0 && Number.isSafeInteger(n) ? n : Number.NaN;
}

/** "" → null; otherwise a non-negative number or NaN. Accepts a leading "$". */
export function parseBudget(s: string): number | null {
  const t = s.trim().replace(/^\$/, "").replace(/,/g, "");
  if (t === "") return null;
  if (!/^\d*\.?\d+$/.test(t)) return Number.NaN;
  const n = Number(t);
  return n >= 0 ? n : Number.NaN;
}

export function validateForm(f: KeyForm): FormErrors {
  const e: FormErrors = {};
  if (!f.name.trim()) e.name = "Name is required.";
  if (Number.isNaN(parseLimit(f.rpm))) e.rpm = "A whole number above 0, or empty for no limit.";
  if (Number.isNaN(parseLimit(f.tpm))) e.tpm = "A whole number above 0, or empty for no limit.";
  if (Number.isNaN(parseBudget(f.budget))) e.budget = "A dollar amount, or empty for no budget.";
  return e;
}

export function createPayload(f: KeyForm): KeyCreateInput {
  const out: KeyCreateInput = { name: f.name.trim() };
  if (f.project.trim()) out.project = f.project.trim();
  const models = parseModels(f.allowedModels);
  if (models.length) out.allowed_models = models;
  const rpm = parseLimit(f.rpm);
  const tpm = parseLimit(f.tpm);
  const budget = parseBudget(f.budget);
  if (rpm !== null) out.rpm_limit = rpm;
  if (tpm !== null) out.tpm_limit = tpm;
  if (budget !== null) out.monthly_budget_usd = budget;
  return out;
}

/**
 * Minimal PATCH body: only fields that changed. Emptying a limit that was
 * set sends the explicit clear_* flag (the API treats a missing field as
 * "unchanged", so null alone can't clear it).
 */
export function updatePayload(orig: ApiKey, f: KeyForm): KeyUpdateInput {
  const out: KeyUpdateInput = {};
  const name = f.name.trim();
  if (name && name !== orig.name) out.name = name;

  const models = parseModels(f.allowedModels);
  const a = [...orig.allowed_models].sort().join("\n");
  const b = [...models].sort().join("\n");
  if (a !== b) out.allowed_models = models;

  const rpm = parseLimit(f.rpm);
  if (rpm !== orig.rpm_limit) {
    if (rpm === null) out.clear_rpm_limit = true;
    else out.rpm_limit = rpm;
  }
  const tpm = parseLimit(f.tpm);
  if (tpm !== orig.tpm_limit) {
    if (tpm === null) out.clear_tpm_limit = true;
    else out.tpm_limit = tpm;
  }
  const budget = parseBudget(f.budget);
  if (budget !== orig.monthly_budget_usd) {
    if (budget === null) out.clear_monthly_budget = true;
    else out.monthly_budget_usd = budget;
  }
  return out;
}
