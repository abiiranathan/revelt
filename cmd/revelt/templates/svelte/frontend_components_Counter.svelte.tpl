<!-- @mode hydrate -->
<script lang="ts">
  interface Props {
    title: string;
    initial?: number;
  }

  let { title, initial = 0 }: Props = $props();
  let count = $state(initial);
</script>

<div class="counter" style="border: 1px solid #ccc; padding: 1rem; margin: 1rem 0;">
  <h3>{title}</h3>
  <p class="counter__value">Count: {count}</p>
  <button type="button" onclick={() => count--}>-</button>
  <button type="button" onclick={() => count++}>+</button>
</div>
