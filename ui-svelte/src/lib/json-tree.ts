export function formatValue(val: unknown): string {
  if (val === null) return "null";
  if (val === undefined) return "undefined";
  if (typeof val === "string") return `"${val}"`;
  if (typeof val === "number" || typeof val === "boolean") return String(val);
  return String(val);
}

export function getType(val: unknown): "object" | "array" | "primitive" {
  if (val === null) return "primitive";
  if (Array.isArray(val)) return "array";
  return typeof val === "object" ? "object" : "primitive";
}

export function getSnippet(val: unknown, maxLen = 200): string {
  const formatted = JSON.stringify(val, null, 2);
  if (formatted.length <= maxLen) return formatted;
  return formatted.slice(0, maxLen) + "\u2026";
}

export function getNodeSnippet(val: unknown): string {
  const type = getType(val);
  if (type === "primitive") return formatValue(val);
  if (type === "object") {
    const entries = Object.entries(val as Record<string, unknown>);
    return `{ ${entries.length} ${entries.length === 1 ? "key" : "keys"} }`;
  }
  return `[ ${(val as unknown[]).length} ${(val as unknown[]).length === 1 ? "item" : "items"} ]`;
}
