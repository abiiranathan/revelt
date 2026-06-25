<!-- @mode ssr -->
<script lang="ts">
  interface Props {
    title: string;
  }
  let { title }: Props = $props();
</script>

<header style="background: linear-gradient(135deg, #0f172a 0%, #1e293b 100%); color: #f8fafc; padding: 3rem 1.5rem; border-bottom: 1px solid #334155; text-align: center; box-shadow: 0 4px 20px rgba(0,0,0,0.08);">
  <div style="max-width: 960px; margin: 0 auto; padding: 0 1.5rem;">
    <h1 style="font-size: 2.5rem; font-weight: 800; letter-spacing: -0.025em; margin-bottom: 0.75rem; background: linear-gradient(to right, #818cf8, #c084fc); -webkit-background-clip: text; -webkit-text-fill-color: transparent;">
      {title}
    </h1>
    <div style="display: inline-flex; align-items: center; gap: 0.5rem; background: rgba(99, 102, 241, 0.1); border: 1px solid rgba(99, 102, 241, 0.2); border-radius: 9999px; padding: 0.25rem 0.75rem; font-size: 0.875rem; color: #a5b4fc; font-weight: 500;">
      <span style="width: 6px; height: 6px; background: #4ade80; border-radius: 50%; display: inline-block;"></span>
      revelt server-rendered component (no client overhead)
    </div>
  </div>
</header>
