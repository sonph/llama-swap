import { describe, it, expect } from "vitest";
import { formatValue, getType, getSnippet, getNodeSnippet } from "./json-tree";

describe("formatValue", () => {
  it("handles null", () => {
    expect(formatValue(null)).toBe("null");
  });

  it("handles undefined", () => {
    expect(formatValue(undefined)).toBe("undefined");
  });

  it("wraps strings in quotes", () => {
    expect(formatValue("hello")).toBe('"hello"');
  });

  it("handles numbers", () => {
    expect(formatValue(42)).toBe("42");
    expect(formatValue(3.14)).toBe("3.14");
  });

  it("handles booleans", () => {
    expect(formatValue(true)).toBe("true");
    expect(formatValue(false)).toBe("false");
  });

  it("handles objects by converting to string", () => {
    expect(formatValue({})).toBe("[object Object]");
  });
});

describe("getType", () => {
  it("returns primitive for null", () => {
    expect(getType(null)).toBe("primitive");
  });

  it("returns primitive for undefined", () => {
    expect(getType(undefined)).toBe("primitive");
  });

  it("returns primitive for strings", () => {
    expect(getType("hello")).toBe("primitive");
  });

  it("returns primitive for numbers", () => {
    expect(getType(42)).toBe("primitive");
  });

  it("returns primitive for booleans", () => {
    expect(getType(true)).toBe("primitive");
  });

  it("returns object for plain objects", () => {
    expect(getType({})).toBe("object");
    expect(getType({ a: 1 })).toBe("object");
  });

  it("returns array for arrays", () => {
    expect(getType([])).toBe("array");
    expect(getType([1, 2, 3])).toBe("array");
  });
});

describe("getSnippet", () => {
  it("returns full JSON for small values", () => {
    const result = getSnippet({ a: 1 }, 200);
    expect(result).toBe(JSON.stringify({ a: 1 }, null, 2));
  });

  it("truncates long JSON with ellipsis", () => {
    const longObj = { a: 1, b: 2, c: 3, d: 4, e: 5, f: 6, g: 7, h: 8, i: 9, j: 10 };
    const result = getSnippet(longObj, 30);
    expect(result.length).toBeLessThan(JSON.stringify(longObj, null, 2).length);
    expect(result).toContain("\u2026");
  });

  it("does not truncate when value fits within maxLen", () => {
    const result = getSnippet({ a: 1 }, 1000);
    expect(result).toBe(JSON.stringify({ a: 1 }, null, 2));
    expect(result).not.toContain("\u2026");
  });

  it("handles arrays", () => {
    const result = getSnippet([1, 2, 3]);
    expect(result).toBe(JSON.stringify([1, 2, 3], null, 2));
  });

  it("handles null", () => {
    expect(getSnippet(null)).toBe("null");
  });
});

describe("getNodeSnippet", () => {
  it("returns formatted value for primitives", () => {
    expect(getNodeSnippet("hello")).toBe('"hello"');
    expect(getNodeSnippet(42)).toBe("42");
    expect(getNodeSnippet(true)).toBe("true");
    expect(getNodeSnippet(null)).toBe("null");
  });

  it("shows object key count", () => {
    expect(getNodeSnippet({})).toBe("{ 0 keys }");
    expect(getNodeSnippet({ a: 1 })).toBe("{ 1 key }");
    expect(getNodeSnippet({ a: 1, b: 2 })).toBe("{ 2 keys }");
    expect(getNodeSnippet({ a: 1, b: 2, c: 3 })).toBe("{ 3 keys }");
  });

  it("shows array item count", () => {
    expect(getNodeSnippet([])).toBe("[ 0 items ]");
    expect(getNodeSnippet([1])).toBe("[ 1 item ]");
    expect(getNodeSnippet([1, 2, 3])).toBe("[ 3 items ]");
  });

  it("handles nested structures", () => {
    const nested = { a: { b: { c: 1 } } };
    const result = getNodeSnippet(nested);
    expect(result).toBe("{ 1 key }");
  });
});
