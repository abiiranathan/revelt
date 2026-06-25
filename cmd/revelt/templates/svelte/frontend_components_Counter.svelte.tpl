<!-- @mode hydrate -->
<script lang="ts">
  interface Props {
    title: string;
    initial?: number;
  }

  let { title, initial = 0 }: Props = $props();
  let count = $derived(initial);
</script>

<div class="card">
  <div class="header-row">
    <div>
      <h3 class="card-title">{title}</h3>
      <p class="card-subtitle">Interactive state preserved and hydrated dynamically.</p>
    </div>
    <span class="badge">Hydrated</span>
  </div>

  <div class="counter-display">
    <div class="display-label">Current Value</div>
    <div class="display-value">{count}</div>
  </div>

  <div class="button-row">
    <button type="button" class="btn btn-secondary" onclick={() => count--}>-</button>
    <button type="button" class="btn btn-primary" onclick={() => count++}>+</button>
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
    background: #f1f5f9;
    color: #64748b;
    padding: 0.125rem 0.5rem;
    border-radius: 9999px;
    font-weight: 600;
    text-transform: uppercase;
  }

  .counter-display {
    background: #f8fafc;
    border-radius: 12px;
    padding: 1.5rem;
    text-align: center;
    border: 1px solid #f1f5f9;
  }

  .display-label {
    font-size: 0.75rem;
    text-transform: uppercase;
    letter-spacing: 0.05em;
    color: #94a3b8;
    font-weight: 600;
    margin-bottom: 0.25rem;
  }

  .display-value {
    font-size: 2.75rem;
    font-weight: 800;
    color: #0f172a;
    font-family: monospace;
  }

  .button-row {
    display: flex;
    gap: 0.75rem;
  }

  .btn {
    flex: 1;
    padding: 0.75rem;
    font-weight: 600;
    border-radius: 8px;
    cursor: pointer;
    font-size: 1.125rem;
    display: inline-flex;
    justify-content: center;
    align-items: center;
    transition: all 0.15s ease;
  }

  .btn-secondary {
    background: #ffffff;
    border: 1px solid #cbd5e1;
    color: #334155;
  }

  .btn-secondary:hover {
    border-color: #6366f1;
    color: #6366f1;
  }

  .btn-primary {
    background: #6366f1;
    border: 1px solid transparent;
    color: #ffffff;
    box-shadow: 0 2px 4px rgba(99, 102, 241, 0.2);
  }

  .btn-primary:hover {
    background: #4f46e5;
  }
</style>
