declare module 'revelt:registry' {
    import { ComponentType } from 'react';
    export interface RegistryEntry {
        Component: ComponentType<any>;
        mode: 'ssr' | 'hydrate' | 'client';
    }
    export const COMPONENT_REGISTRY: Map<string, RegistryEntry>;
}