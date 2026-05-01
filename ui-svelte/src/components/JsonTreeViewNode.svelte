<script lang="ts">
  import JsonTreeViewNode from "./JsonTreeViewNode.svelte";

  interface Props {
    path: string;
    data: unknown;
    indent: number;
    expandAll: boolean;
  }

  let { path, data, indent, expandAll }: Props = $props();

  let userExpanded = $state(false);
  let wasToggled = $state(false);

  $effect(() => {
    if (expandAll && !wasToggled) {
      userExpanded = true;
    }
    if (!expandAll) {
      // Only collapse if user hasn't manually toggled
      if (!wasToggled) {
        userExpanded = false;
      }
    }
  });

  function toggle() {
    wasToggled = true;
    userExpanded = !userExpanded;
  }

  function getType(val: unknown): "object" | "array" | "primitive" {
    if (val === null) return "primitive";
    if (Array.isArray(val)) return "array";
    return typeof val === "object" ? "object" : "primitive";
  }

  function formatValue(val: unknown): string {
    if (val === null) return "null";
    if (val === undefined) return "undefined";
    if (typeof val === "string") return `"${val}"`;
    if (typeof val === "number" || typeof val === "boolean") return String(val);
    return String(val);
  }

  function getNodeSnippet(val: unknown): string {
    const type = getType(val);
    if (type === "primitive") return formatValue(val);
    if (type === "object") {
      const entries = Object.entries(val as Record<string, unknown>);
      return `{ ${entries.length} ${entries.length === 1 ? "key" : "keys"} }`;
    }
    return `[ ${(val as unknown[]).length} ${(val as unknown[]).length === 1 ? "item" : "items"} ]`;
  }

  let type = $derived(getType(data));
  let isLeaf = $derived(type === "primitive");
  let isExpandable = $derived(!isLeaf);
  let showChildren = $derived(userExpanded && isExpandable);

  let dataArray = $derived.by(() => {
    if (type === "array") return data as unknown[];
    return [];
  });
</script>

<div>
  <div
    class="flex items-start hover:bg-background/30 rounded cursor-pointer transition-colors"
    class:cursor-default={isLeaf}
    role="button"
    tabindex={isLeaf ? -1 : 0}
    onkeydown={(e) => { if (isExpandable && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); toggle(); }}}
    onclick={toggle}
  >
    {#if isExpandable}
      <span class="text-txtsecondary select-none w-4 shrink-0">
        {#if userExpanded}▼{:else}▶{/if}
      </span>
    {:else}
      <span class="w-4 shrink-0"></span>
    {/if}
    <span class="text-primary">"{path}"</span>:
    {#if isExpandable}
      <span class="text-txtsecondary ml-1">
        {#if type === "object"}{getNodeSnippet(data)}{:else}[{getNodeSnippet(data)}]{/if}
      </span>
    {:else}
      <span class="ml-1">
        {#if typeof data === "string"}
          <span class="text-green-500">{formatValue(data)}</span>
        {:else if typeof data === "number"}
          <span class="text-orange-400">{formatValue(data)}</span>
        {:else if typeof data === "boolean"}
          <span class="text-purple-400">{formatValue(data)}</span>
        {:else}
          <span class="text-txtsecondary">{formatValue(data)}</span>
        {/if}
      </span>
    {/if}
  </div>

  {#if showChildren}
<div>
      {#if type === "object"}
        {#each Object.entries(data as Record<string, unknown>) as [key, value]}
          <div style="padding-left: {(indent + 1) * 20}px">
            <JsonTreeViewNode path={key} data={value} indent={indent + 1} expandAll={expandAll} />
          </div>
        {/each}
      {:else}
        {#each dataArray as item, index}
          <div style="padding-left: {(indent + 1) * 20}px">
            <JsonTreeViewNode path={String(index)} data={item} indent={indent + 1} expandAll={expandAll} />
          </div>
        {/each}
      {/if}
    </div>
  {/if}
</div>
