<script lang="ts">
  import { persistentStore } from "../stores/persistent";
  import ScatterPlot from "./ScatterPlot.svelte";
  import { scatterData } from "../stores/activityStats";

  const scatterCollapsed = persistentStore<boolean>("activity-scatter-collapsed", false);

  const modelColors = [
    "#3b82f6", "#10b981", "#f59e0b", "#8b5cf6",
    "#f43f5e", "#06b6d4", "#6366f1", "#f97316",
  ];

  let modelList = $derived(
    $scatterData ? Array.from(new Set($scatterData.points.map((p) => p.model))).sort() : []
  );
</script>

<div class="mt-2">
  <!-- Toggle button (above chart, right-aligned) -->
  <div class="flex justify-end">
    <button
      class="px-3 py-1 text-xs font-medium rounded-md border border-gray-300 dark:border-gray-600 text-gray-500 dark:text-gray-400 hover:text-gray-700 dark:hover:text-gray-200 hover:border-gray-400 dark:hover:border-gray-500 transition-colors"
      onclick={() => ($scatterCollapsed = !$scatterCollapsed)}
    >
      {#if $scatterCollapsed}
        Show TPS vs Context Size
      {:else}
        Hide TPS vs Context Size
      {/if}
    </button>
  </div>

  <!-- Chart + legend row (hidden when collapsed) -->
  {#if !$scatterCollapsed}
    <div class="mt-4 flex gap-4">
      <!-- Scatter plot chart -->
      <div class="flex-1 min-w-0">
        {#if $scatterData}
          <ScatterPlot
            data={$scatterData}
            xLabel="Total Prompt Size (tokens)"
            yLabel="Gen Speed (tokens/sec)"
          />
        {:else}
          <div class="py-8 text-center text-sm text-gray-500 dark:text-gray-400">
            Not enough data yet — make requests with different context sizes to see the relationship
          </div>
        {/if}
      </div>

      <!-- Model legend -->
      {#if modelList.length > 0}
        <div class="shrink-0 w-36 pt-1">
          <div class="text-xs font-medium text-gray-500 dark:text-gray-400 mb-2">Models</div>
          {#each modelList as modelName, idx}
            <div class="flex items-center gap-2 mb-1.5">
              <span style="background-color: {modelColors[idx % modelColors.length]}" class="w-3 h-3 rounded-full inline-block"></span>
              <span class="text-xs text-gray-700 dark:text-gray-300 truncate" title={modelName}>
                {modelName}
              </span>
            </div>
          {/each}
        </div>
      {/if}
    </div>
  {/if}
</div>
