<script lang="ts">
  import JsonTreeViewNode from "./JsonTreeViewNode.svelte";

  interface Props {
    data: unknown;
    maxSnippet?: number;
  }

  let { data, maxSnippet = 200 }: Props = $props();

  let collapsed = $state(false);
  let expandAll = $state(false);

  function formatValue(val: unknown): string {
    if (val === null) return "null";
    if (val === undefined) return "undefined";
    if (typeof val === "string") return `"${val}"`;
    if (typeof val === "number" || typeof val === "boolean") return String(val);
    return String(val);
  }

  function getType(val: unknown): "object" | "array" | "primitive" {
    if (val === null) return "primitive";
    if (Array.isArray(val)) return "array";
    return typeof val === "object" ? "object" : "primitive";
  }

  function getSnippet(val: unknown): string {
    const formatted = JSON.stringify(val, null, 2);
    if (formatted.length <= maxSnippet) return formatted;
    return formatted.slice(0, maxSnippet) + "\u2026";
  }

  

  let dataArray = $derived.by(() => {
    if (getType(data) === "array") return data as unknown[];
    return [];
  });
</script>

<div class="font-mono text-sm leading-relaxed">
  {#if collapsed}
    <button
      type="button"
      class="w-full text-left cursor-pointer hover:opacity-80 transition-opacity bg-background/50 rounded p-2 block"
      onclick={() => (collapsed = false)}
    >
      <pre class="whitespace-pre-wrap text-txtsecondary text-xs">{getSnippet(data)}</pre>
    </button>
  {:else}
    <div class="mb-1">
      <button
        type="button"
        class="tw-btn"
        onclick={() => (expandAll = !expandAll)}
      >[{expandAll ? "Collapse" : "Expand"} All]</button>
    </div>
    {#if getType(data) === "object"}
      {#each Object.entries(data as Record<string, unknown>) as [key, value]}
        <JsonTreeViewNode path={key} data={value} indent={0} expandAll={expandAll} />
      {/each}
    {:else if getType(data) === "array"}
      {#each dataArray as item, index}
        <JsonTreeViewNode path={String(index)} data={item} indent={0} expandAll={expandAll} />
      {/each}
    {:else}
      <div><span class="text-txtsecondary">{formatValue(data)}</span></div>
    {/if}
  {/if}
</div>

<style>
  pre {
    margin: 0;
  }
  :global(.tw-btn) {
    padding: 2px 10px;
    font-size: 0.75rem;
    border-radius: 4px;
    color: var(--color-txtsecondary);
    cursor: pointer;
    border: 1px solid transparent;
    background: transparent;
    transition: all 0.15s;
  }
  :global(.tw-btn:hover) {
    color: var(--color-txtmain);
    background: var(--color-secondary);
  }
</style>
