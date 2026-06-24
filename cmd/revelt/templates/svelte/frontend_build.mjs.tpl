import { build } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { resolve, dirname, basename, extname } from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

const __dirname = dirname(fileURLToPath(import.meta.url));

const configPath = resolve(__dirname, '../revelt.json');
const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));

// Component directory relative to the frontend source directory,
// defaulting to "components" when the key is absent from config.
const componentDirName = config.component_dir ?? 'components';
const componentDir = resolve(__dirname, componentDirName);

/** @typedef {'ssr' | 'hydrate' | 'client'} ComponentMode */

/**
 * Reads the leading lines of a source file and extracts the declared
 * rendering mode from a `@mode <ssr|hydrate|client>` comment annotation.
 * Falls back to `'hydrate'` when no annotation is present.
 *
 * @param {string} filePath Absolute path to the component source file.
 * @returns {ComponentMode}
 */
function readModeAnnotation(filePath) {
    const SEARCH_LINES = 5;
    const source = fs.readFileSync(filePath, 'utf8');
    const lines = source.split('\n', SEARCH_LINES);
    for (const line of lines) {
        // Matches annotations in any comment style: //, /*, or * (JSDoc line)
        const m = line.match(/@mode\s+(ssr|hydrate|client)/);
        if (m) {
            return /** @type {ComponentMode} */ (m[1]);
        }
    }
    return 'hydrate';
}

/**
 * Discovers all component files inside `componentDir` and returns metadata
 * for each one. Only .svelte files are included.
 *
 * @returns {{ name: string, path: string, mode: ComponentMode }[]}
 */
function discoverComponents() {
    if (!fs.existsSync(componentDir)) {
        console.error(`[revelt] component directory not found: ${componentDir}`);
        return [];
    }

    return fs
        .readdirSync(componentDir)
        .filter((file) => extname(file) === '.svelte')
        .map((file) => {
            const absPath = resolve(componentDir, file);
            const name = basename(file, extname(file));
            const mode = readModeAnnotation(absPath);
            return { name, path: `./${componentDirName}/${file}`, mode };
        });
}

const components = discoverComponents();

console.error(
    `[revelt] discovered ${components.length} component(s): ` +
    components.map((c) => `${c.name}(${c.mode})`).join(', ')
);

const serverComponents = components.filter(
    (c) => c.mode === 'ssr' || c.mode === 'hydrate'
);
const clientComponents = components.filter(
    (c) => c.mode === 'hydrate' || c.mode === 'client'
);

const watchMode = process.argv.includes('--watch');

/**
 * Vite plugin that provides a virtual `revelt:registry` module whose
 * contents are generated from the supplied component list.
 *
 * @param {{ name: string, path: string, mode: ComponentMode }[]} comps
 * @returns {import('vite').Plugin}
 */
function componentRegistryPlugin(comps) {
    const virtualId = 'revelt:registry';
    const resolvedId = '\0SSRevelt:registry';

    return {
        name: 'revelt-component-registry',
        resolveId(id) {
            if (id === virtualId) return resolvedId;
        },
        load(id) {
            if (id !== resolvedId) return;

            if (comps.length === 0) {
                return 'export const COMPONENT_REGISTRY = new Map();';
            }

            const imports = comps
                .map((c) =>
                    'import _c' + c.name +
                    ' from ' + JSON.stringify(resolve(__dirname, c.path)) + ';'
                )
                .join('\n');

            const entries = comps
                .map((c) =>
                    '  [' + JSON.stringify(c.name) +
                    ', { Component: _c' + c.name +
                    ', mode: ' + JSON.stringify(c.mode) + ' }],'
                )
                .join('\n');

            return (
                imports +
                '\nexport const COMPONENT_REGISTRY = new Map([\n' +
                entries +
                '\n]);'
            );
        },
    };
}

/** @type {import('vite').UserConfig} */
const serverConfig = {
    plugins: [
        svelte(),
        componentRegistryPlugin(serverComponents),
    ],
    ssr: {
        noExternal: ['revelt:registry'],
    },
    build: {
        ssr: true,
        rollupOptions: {
            input: 'render-server.js',
            output: {
                format: 'cjs',
                entryFileNames: 'render-server.cjs',
            },
        },
        outDir: 'dist',
        minify: false,
        sourcemap: watchMode,
    },
};

/** @type {import('vite').UserConfig} */
const clientConfig = {
    plugins: [
        svelte(),
        componentRegistryPlugin(clientComponents),
    ],
    build: {
        minify: !watchMode,
        sourcemap: watchMode,
        outDir: 'dist/client',
        emptyOutDir: false,
        rollupOptions: {
            input: 'client.js',
            output: {
                format: 'iife',
                entryFileNames: 'client.js',
            },
        },
    },
};

if (watchMode) {
    await build({ ...serverConfig, build: { ...serverConfig.build, watch: {} } });
    await build({ ...clientConfig, build: { ...clientConfig.build, watch: {} } });
    console.error('[revelt] watching frontend files for changes...');
} else {
    await build(serverConfig);
    await build(clientConfig);
    console.error('[revelt] built → dist/render-server.cjs and dist/client/client.js');
}
