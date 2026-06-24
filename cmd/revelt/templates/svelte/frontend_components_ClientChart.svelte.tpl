<!-- @mode client -->
<script lang="ts">
  import { onMount } from "svelte";

  interface Props {
    label: string;
  }

  let { label }: Props = $props();
  let data: number[] = $state([]);

  onMount(() => {
    data = [12, 19, 3, 5, 2, 3];
  });
</script>

<div style="border: 2px dashed #ff007f; padding: 1rem; margin: 1rem 0;">
  <h3>{label}</h3>
  <p>Rendered client-side only (bypasses Svelte server rendering completely).</p>
  <div style="display: flex; gap: 10px; align-items: flex-end; height: 100px; margin-top: 1rem;">
    {#each data as val}
      <div
        style="width: 30px; height: {val * 4}px; background-color: #ff007f; text-align: center; color: #fff; font-size: 10px;"
      >
        {val}
      </div>
    {/each}
  </div>
</div>
