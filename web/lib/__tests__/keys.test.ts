import { describe, expect, it } from "vitest";
import type { ApiKey } from "../api";
import { createPayload, emptyForm, formFromKey, parseBudget, parseLimit, parseModels, updatePayload, validateForm } from "../keys";

const key: ApiKey = {
  id: "k1",
  project_id: "p1",
  project: "demo",
  name: "dash",
  prefix: "sy-abcd",
  allowed_models: ["a", "b"],
  rpm_limit: 600,
  tpm_limit: null,
  monthly_budget_usd: 25,
  spent_this_month_usd: 1.2,
  disabled: false,
  created_at: "2026-10-01T00:00:00Z",
  last_used_at: null,
};

describe("parsers", () => {
  it("parses model lists", () => expect(parseModels(" a, b\nc  a ")).toEqual(["a", "b", "c"]));
  it("parses limits", () => {
    expect(parseLimit("")).toBeNull();
    expect(parseLimit("1,000")).toBe(1000);
    expect(parseLimit("0")).toBeNaN();
    expect(parseLimit("1.5")).toBeNaN();
    expect(parseLimit("abc")).toBeNaN();
  });
  it("parses budgets", () => {
    expect(parseBudget("")).toBeNull();
    expect(parseBudget("$12.50")).toBe(12.5);
    expect(parseBudget("0")).toBe(0);
    expect(parseBudget("-1")).toBeNaN();
  });
});

describe("validateForm", () => {
  it("requires a name and valid numbers", () => {
    const e = validateForm({ ...emptyForm(), rpm: "x", budget: "y" });
    expect(Object.keys(e).sort()).toEqual(["budget", "name", "rpm"]);
  });
});

describe("createPayload", () => {
  it("omits empty optional fields", () => {
    expect(createPayload({ ...emptyForm(), name: " svc " })).toEqual({ name: "svc", project: "default" });
  });
  it("includes limits and models", () => {
    expect(createPayload({ name: "svc", project: "demo", allowedModels: "m1, m2", rpm: "60", tpm: "1000", budget: "5" })).toEqual({
      name: "svc",
      project: "demo",
      allowed_models: ["m1", "m2"],
      rpm_limit: 60,
      tpm_limit: 1000,
      monthly_budget_usd: 5,
    });
  });
});

describe("updatePayload", () => {
  it("is empty when nothing changed (model order ignored)", () => {
    const f = formFromKey(key);
    expect(updatePayload(key, { ...f, allowedModels: "b, a" })).toEqual({});
  });
  it("sends clear flags when a limit is emptied", () => {
    const f = { ...formFromKey(key), rpm: "", budget: "" };
    expect(updatePayload(key, f)).toEqual({ clear_rpm_limit: true, clear_monthly_budget: true });
  });
  it("sends only changed values", () => {
    const f = { ...formFromKey(key), name: "dash2", tpm: "5000", allowedModels: "" };
    expect(updatePayload(key, f)).toEqual({ name: "dash2", tpm_limit: 5000, allowed_models: [] });
  });
});
