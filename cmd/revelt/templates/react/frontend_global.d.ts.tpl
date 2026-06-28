declare module 'revelt:registry' {
    import { ComponentType } from 'react';

    /**
     * A registry entry for an eager component (mode 'hydrate' or 'client').
     * Component is available synchronously at module evaluation time.
     */
    interface EagerEntry {
        Component: ComponentType<any>;
        load?: never;
        mode: 'ssr' | 'hydrate' | 'client';
    }

    /**
     * A registry entry for a lazy component (mode 'lazy-client').
     * Component is undefined until load() resolves. The chunk is fetched
     * on the first call to load() — i.e. when the island enters the DOM.
     */
    interface LazyEntry {
        Component?: never;
        load: () => Promise<ComponentType<any>>;
        mode: 'lazy-client';
    }

    export type RegistryEntry = EagerEntry | LazyEntry;

    export const COMPONENT_REGISTRY: Map<string, RegistryEntry>;
}
