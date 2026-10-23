import { describe, expect, it } from "vitest";
import { mergeNewest } from "../live";

describe("mergeNewest", () => {
  it("puts the newest event first", () => {
    expect(mergeNewest([3, 2, 1], [4, 5])).toEqual([5, 4, 3, 2, 1]);
  });
  it("caps the list", () => {
    expect(mergeNewest([3, 2, 1], [4, 5], 3)).toEqual([5, 4, 3]);
    expect(mergeNewest([], [1, 2, 3, 4, 5], 2)).toEqual([5, 4]);
  });
  it("returns the same array when nothing arrives", () => {
    const cur = [1];
    expect(mergeNewest(cur, [])).toBe(cur);
  });
});
