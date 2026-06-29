import * as React from "react";
import { hydrateRoot, createRoot } from "react-dom/client";
import { COMPONENT_REGISTRY } from "revelt:registry";
import type { RegistryEntry } from "revelt:registry";

// ---------------------------------------------------------------------------
// Island hydration
// ---------------------------------------------------------------------------

async function resolveComponent(entry: RegistryEntry): Promise<React.ComponentType<any>> {
  if (entry.Component) {
    return entry.Component;
  }
  return entry.load();
}

async function hydrateIsland(el: HTMLElement): Promise<void> {
  if (el.dataset.rssrMounted === "true") return;

  const name = el.getAttribute("data-ssr-island");
  if (!name) return;

  const propsAttr = el.getAttribute("data-ssr-props");
  const props = propsAttr ? (JSON.parse(propsAttr) as Record<string, unknown>) : {};

  const entry = COMPONENT_REGISTRY.get(name);
  if (!entry) {
    console.warn(`[revelt] component "${name}" not found in registry.`);
    return;
  }

  // Resolves instantly for eager components; dynamically fetches chunk for route-split components
  const Component = await resolveComponent(entry);
  if (entry.mode === "hydrate") {
    hydrateRoot(el, React.createElement(Component, props));
  } else {
    const root = createRoot(el);
    root.render(React.createElement(Component, props));
  }

  el.dataset.rssrMounted = "true";
}

async function hydrateIslands(root: Element | Document): Promise<void> {
  const islands = root.querySelectorAll("[data-ssr-island]") as NodeListOf<HTMLElement>;
  for (const el of islands) {
    void hydrateIsland(el);
  }
}

// Observes route changes or AJAX content replacements dynamically
function observeIslands(root: Element | Document): void {
  const observer = new MutationObserver((mutations) => {
    for (const mutation of mutations) {
      if (mutation.type === "childList") {
        mutation.addedNodes.forEach((node) => {
          if (node instanceof Element) {
            if (node.hasAttribute("data-ssr-island")) {
              void hydrateIsland(node as HTMLElement);
            }
            const islands = node.querySelectorAll("[data-ssr-island]");
            islands.forEach((island) => void hydrateIsland(island as HTMLElement));
          }
        });
      }
    }
  });

  observer.observe(root, { childList: true, subtree: true });
}

// ---------------------------------------------------------------------------
// History API router (Optional Fallback)
// ---------------------------------------------------------------------------

async function fetchPage(url: string): Promise<string> {
  const res = await fetch(url, {
    headers: { Accept: "text/html" },
    credentials: "same-origin",
  });
  if (!res.ok) throw new Error(`[revelt] navigation to ${url} failed: ${res.status}`);
  const text = await res.text();
  const doc = new DOMParser().parseFromString(text, "text/html");
  return doc.body.innerHTML;
}

async function navigate(url: string): Promise<void> {
  try {
    const bodyHTML = await fetchPage(url);
    document.body.innerHTML = bodyHTML;
    history.pushState({ revelt: true, url }, "", url);
    await hydrateIslands(document.body);
  } catch (err) {
    console.error(err);
    location.href = url;
  }
}

function interceptLinks(): void {
  document.addEventListener("click", (e: MouseEvent) => {
    // Gracefully ignore clicks intercepted by external routers (e.g. React Router)
    if (e.defaultPrevented) return;

    const target = e.target;
    if (!(target instanceof Element)) return;

    const anchor = target.closest("a");
    if (!anchor) return;

    const href = anchor.getAttribute("href");
    if (!href) return;

    if (
      anchor.dataset.reload !== undefined ||
      anchor.target ||
      anchor.download ||
      href.startsWith("http") ||
      href.startsWith("//") ||
      href.startsWith("mailto:") ||
      href.startsWith("#")
    ) {
      return;
    }

    e.preventDefault();
    void navigate(href);
  });
}

function handlePopState(): void {
  window.addEventListener("popstate", (e: PopStateEvent) => {
    // Only intercept if we pushed the state to avoid breaking other client history trees
    if (e.state && e.state.revelt) {
      void navigate(location.pathname + location.search);
    }
  });
}

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------

async function bootstrap(): Promise<void> {
  history.replaceState({ revelt: true, url: location.pathname + location.search }, "");

  await hydrateIslands(document);
  observeIslands(document.body);

  interceptLinks();
  handlePopState();
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", () => {
    void bootstrap();
  });
} else {
  void bootstrap();
}
