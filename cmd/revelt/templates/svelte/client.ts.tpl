{{TAILWIND_CSS_IMPORT}}import { hydrate, mount } from 'svelte';
import type { Component } from 'svelte';
import { COMPONENT_REGISTRY } from 'revelt:registry';

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

interface EagerEntry {
    Component: Component<any>;
    load?: never;
    mode: 'ssr' | 'hydrate' | 'client';
}

interface LazyEntry {
    Component?: never;
    load: () => Promise<Component<any>>;
    mode: 'lazy-client';
}

type RegistryEntry = EagerEntry | LazyEntry;

// ---------------------------------------------------------------------------
// Island hydration
// ---------------------------------------------------------------------------

async function resolveComponent(entry: RegistryEntry): Promise<Component<any>> {
    if (entry.Component) {
        return entry.Component;
    }
    return entry.load();
}

async function hydrateIsland(el: HTMLElement): Promise<void> {
    if (el.dataset.rssrMounted === 'true') return;

    const name = el.getAttribute('data-ssr-island');
    if (!name) return;

    const propsAttr = el.getAttribute('data-ssr-props');
    const props: Record<string, unknown> = propsAttr ? JSON.parse(propsAttr) : {};

    const entry = COMPONENT_REGISTRY.get(name);
    if (!entry) {
        console.warn(`[revelt] component "${name}" not found in registry.`);
        return;
    }

    const Component = await resolveComponent(entry);
    if (entry.mode === 'hydrate') {
        hydrate(Component, { target: el, props });
    } else {
        mount(Component, { target: el, props });
    }

    el.dataset.rssrMounted = 'true';
}

async function hydrateIslands(root: Element | Document): Promise<void> {
    const islands = root.querySelectorAll<HTMLElement>('[data-ssr-island]');
    for (const el of islands) {
        void hydrateIsland(el);
    }
}

function observeIslands(root: Element | Document): void {
    const observer = new MutationObserver((mutations: MutationRecord[]) => {
        for (const mutation of mutations) {
            if (mutation.type === 'childList') {
                mutation.addedNodes.forEach((node) => {
                    if (node instanceof Element) {
                        if (node.hasAttribute('data-ssr-island')) {
                            void hydrateIsland(node as HTMLElement);
                        }
                        node.querySelectorAll<HTMLElement>('[data-ssr-island]')
                            .forEach((island) => void hydrateIsland(island));
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
        headers: { Accept: 'text/html' },
        credentials: 'same-origin',
    });
    if (!res.ok) throw new Error(`[revelt] navigation to ${url} failed: ${res.status}`);
    const text = await res.text();
    const doc = new DOMParser().parseFromString(text, 'text/html');
    return doc.body.innerHTML;
}

async function navigate(url: string): Promise<void> {
    try {
        const bodyHTML = await fetchPage(url);
        document.body.innerHTML = bodyHTML;
        history.pushState({ revelt: true, url }, '', url);
        await hydrateIslands(document.body);
    } catch (err) {
        console.error(err);
        location.href = url;
    }
}

function interceptLinks(): void {
    document.addEventListener('click', (e: MouseEvent) => {
        if (e.defaultPrevented) return;

        const target = e.target;
        if (!(target instanceof Element)) return;

        const anchor = target.closest('a');
        if (!anchor) return;

        const href = anchor.getAttribute('href');
        if (!href) return;

        if (
            anchor.dataset.reload !== undefined ||
            anchor.target ||
            anchor.download ||
            href.startsWith('http') ||
            href.startsWith('//') ||
            href.startsWith('mailto:') ||
            href.startsWith('#')
        ) {
            return;
        }

        e.preventDefault();
        navigate(href);
    });
}

function handlePopState(): void {
    window.addEventListener('popstate', (e: PopStateEvent) => {
        if (e.state && (e.state as { revelt?: boolean }).revelt) {
            navigate(location.pathname + location.search);
        }
    });
}

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------

async function bootstrap(): Promise<void> {
    history.replaceState({ revelt: true, url: location.pathname + location.search }, '');

    await hydrateIslands(document);
    observeIslands(document.body);

    interceptLinks();
    handlePopState();
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', () => void bootstrap());
} else {
    void bootstrap();
}
