<!-- @mode ssr -->
<script lang="ts">
  interface Props {
    title: string;
  }
  let { title }: Props = $props();
</script>

<header style="background: #333; color: #fff; padding: 1rem;">
  <h1>{title}</h1>
  <small>Note: This Svelte header is server-rendered only (no client JS loaded).</small>
</header>
