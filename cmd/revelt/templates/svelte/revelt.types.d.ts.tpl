declare module 'revelt:registry' {
    import type { Component } from 'svelte';

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

    export type RegistryEntry = EagerEntry | LazyEntry;
    export const COMPONENT_REGISTRY: Map<string, RegistryEntry>;
}
