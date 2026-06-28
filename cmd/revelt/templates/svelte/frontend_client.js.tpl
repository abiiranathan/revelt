{{TAILWIND_CSS_IMPORT}}import { hydrate, mount } from 'svelte';
import { COMPONENT_REGISTRY } from 'revelt:registry';

// ---------------------------------------------------------------------------
// Island hydration
// ---------------------------------------------------------------------------

async function resolveComponent(entry) {
    if (entry.Component) {
        return entry.Component;
    }
    return entry.load();
}

async function hydrateIsland(el) {
    if (el.dataset.rssrMounted === 'true') return;

    const name = el.getAttribute('data-ssr-island');
    if (!name) return;

    const propsAttr = el.getAttribute('data-ssr-props');
    const props = propsAttr ? JSON.parse(propsAttr) : {};

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

async function hydrateIslands(root) {
    const islands = root.querySelectorAll('[data-ssr-island]');
    for (const el of islands) {
        void hydrateIsland(el);
    }
}

function observeIslands(root) {
    const observer = new MutationObserver((mutations) => {
        for (const mutation of mutations) {
            if (mutation.type === 'childList') {
                mutation.addedNodes.forEach((node) => {
                    if (node instanceof Element) {
                        if (node.hasAttribute('data-ssr-island')) {
                            void hydrateIsland(node);
                        }
                        const islands = node.querySelectorAll('[data-ssr-island]');
                        islands.forEach((island) => void hydrateIsland(island));
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

async function fetchPage(url) {
    const res = await fetch(url, {
        headers: { Accept: 'text/html' },
        credentials: 'same-origin',
    });
    if (!res.ok) throw new Error(`[revelt] navigation to ${url} failed: ${res.status}`);
    const text = await res.text();
    const doc = new DOMParser().parseFromString(text, 'text/html');
    return doc.body.innerHTML;
}

async function navigate(url) {
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

function interceptLinks() {
    document.addEventListener('click', (e) => {
        if (e.defaultPrevented) return;

        const anchor = /** @type {Element} */ (e.target).closest('a');
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

function handlePopState() {
    window.addEventListener('popstate', (e) => {
        if (e.state && e.state.revelt) {
            navigate(location.pathname + location.search);
        }
    });
}

// ---------------------------------------------------------------------------
// Bootstrap
// ---------------------------------------------------------------------------

async function bootstrap() {
    history.replaceState({ revelt: true, url: location.pathname + location.search }, '');
    
    await hydrateIslands(document);
    observeIslands(document.body);

    interceptLinks();
    handlePopState();
}

if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', bootstrap);
} else {
    bootstrap();
}
