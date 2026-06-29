<!-- @mode client -->
<script lang="ts">
  import { onMount } from "svelte";

  interface Props {
    label: string;
  }

  let { label }: Props = $props();
  let data: number[] = $state([]);
  let hoveredIdx: number | null = $state(null);

  onMount(() => {
    data = [12, 19, 3, 5, 2, 8];
  });
</script>

<div class="card">
  <div class="header-row">
    <div>
      <h3 class="card-title">{label}</h3>
      <p class="card-subtitle">Bypasses server-side rendering. Executes client-side.</p>
    </div>
    <span class="badge">Client-Only</span>
  </div>

  <div class="chart-container">
    {#if data.length === 0}
      <div class="loading-state">Loading component...</div>
    {:else}
      {#each data as val, i}
        <div
          class="bar"
          class:hovered={hoveredIdx === i}
          style="height: {(val / 20) * 100}%;"
          onmouseenter={() => hoveredIdx = i}
          onmouseleave={() => hoveredIdx = null}
          role="img"
          aria-label="Value: {val}"
        >
          <span class="bar-value">{val}</span>
          
          {#if hoveredIdx === i}
            <div class="tooltip">Value: {val}</div>
          {/if}
        </div>
      {/each}
    {/if}
  </div>
</div>

<style>
  .card {
    background: #ffffff;
    border: 1px solid #e2e8f0;
    border-radius: 16px;
    padding: 2rem;
    box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.05), 0 2px 4px -1px rgba(0, 0, 0, 0.03);
    display: flex;
    flex-direction: column;
    gap: 1.25rem;
  }

  .header-row {
    display: flex;
    justify-content: space-between;
    align-items: flex-start;
  }

  .card-title {
    font-size: 1.25rem;
    font-weight: 700;
    color: #1e293b;
    letter-spacing: -0.01em;
    margin-bottom: 0.25rem;
  }

  .card-subtitle {
    font-size: 0.875rem;
    color: #64748b;
  }

  .badge {
    font-size: 0.75rem;
    background: rgba(236, 72, 153, 0.1);
    color: #ec4899;
    padding: 0.125rem 0.5rem;
    border-radius: 9999px;
    font-weight: 600;
    text-transform: uppercase;
  }

  .chart-container {
    background: #faf5ff;
    border-radius: 12px;
    padding: 2rem 1.5rem 1rem;
    border: 1px solid #f3e8ff;
    height: 170px;
    display: flex;
    align-items: flex-end;
    justify-content: space-around;
    gap: 10px;
    position: relative;
  }

  .loading-state {
    color: #a855f7;
    font-size: 0.875rem;
    width: 100%;
    text-align: center;
    align-self: center;
  }

  .bar {
    flex: 1;
    background: linear-gradient(180deg, #ec4899 0%, #8b5cf6 100%);
    border-radius: 6px 6px 0 0;
    transition: all 0.2s ease-out;
    cursor: pointer;
    display: flex;
    justify-content: center;
    align-items: flex-start;
    position: relative;
  }

  .bar.hovered {
    background: linear-gradient(180deg, #d946ef 0%, #a855f7 100%);
    box-shadow: 0 4px 12px rgba(168, 85, 247, 0.4);
    transform: scaleY(1.05);
  }

  .bar-value {
    font-size: 10px;
    color: #ffffff;
    font-weight: 700;
    margin-top: 6px;
    opacity: 0.8;
    transition: opacity 0.2s;
  }

  .bar.hovered .bar-value {
    opacity: 1;
  }

  .tooltip {
    position: absolute;
    bottom: 105%;
    background: #1e293b;
    color: #ffffff;
    padding: 4px 8px;
    border-radius: 4px;
    font-size: 10px;
    font-weight: 600;
    white-space: nowrap;
    box-shadow: 0 2px 4px rgba(0,0,0,0.1);
    z-index: 10;
  }
</style>
