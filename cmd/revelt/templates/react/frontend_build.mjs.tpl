import * as esbuild from 'esbuild';
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
        const m = line.match(/@mode\s+(ssr|hydrate|client)/);
        if (m) {
            return /** @type {ComponentMode} */ (m[1]);
        }
    }
    return 'hydrate';
}

/**
 * Discovers all component files inside `componentDir` and returns metadata
 * for each one. Only files with recognised extensions are included.
 *
 * @returns {{ name: string, path: string, mode: ComponentMode }[]}
 */
function discoverComponents() {
    if (!fs.existsSync(componentDir)) {
        console.error(`[revelt] component directory not found: ${componentDir}`);
        return [];
    }

    const COMPONENT_EXTENSIONS = new Set(['.tsx', '.ts', '.jsx', '.js']);

    return fs
        .readdirSync(componentDir)
        .filter((file) => COMPONENT_EXTENSIONS.has(extname(file)))
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

/** @type {esbuild.BuildOptions} */
const serverBuildOptions = {
    entryPoints: ['render-server.js'],
    bundle: true,
    platform: 'node',
    format: 'cjs',
    outfile: 'dist/render-server.cjs',
    target: 'node18',
    jsx: 'automatic',
    resolveExtensions: ['.tsx', '.ts', '.jsx', '.js', '.json'],
    sourcemap: watchMode ? 'inline' : false,
    logOverride: { 'ignored-bare-import': 'silent' },
    external: ['react', 'react-dom', 'react-dom/server'],
    plugins: [componentRegistryPlugin(serverComponents)],
};

/** @type {esbuild.BuildOptions} */
const clientBuildOptions = {
    entryPoints: ['client.tsx'],
    bundle: true,
    platform: 'browser',
    format: 'iife',
    outfile: 'dist/client/client.js',
    target: 'es2020',
    jsx: 'automatic',
    resolveExtensions: ['.tsx', '.ts', '.jsx', '.js', '.json'],
    sourcemap: watchMode ? 'inline' : false,
    logOverride: { 'ignored-bare-import': 'silent' },
    plugins: [componentRegistryPlugin(clientComponents)],
    minify: !watchMode,
};

/**
 * esbuild plugin that provides a virtual `revelt:registry` module whose
 * contents are generated from the supplied component list.
 *
 * @param {{ name: string, path: string, mode: ComponentMode }[]} comps
 * @returns {import('esbuild').Plugin}
 */
function componentRegistryPlugin(comps) {
    const registryPath = 'revelt:registry';

    return {
        name: 'revelt-component-registry',
        setup(build) {
            build.onResolve({ filter: /^revelt:registry$/ }, () => ({
                path: registryPath,
                namespace: 'revelt-registry',
            }));

            build.onLoad({ filter: /.*/, namespace: 'revelt-registry' }, () => {
                if (comps.length === 0) {
                    return {
                        contents: 'export const COMPONENT_REGISTRY = new Map();',
                        loader: 'js',
                        resolveDir: __dirname,
                    };
                }

                const imports = comps
                    .map((c) =>
                        'import * as _c' + c.name +
                        ' from ' + JSON.stringify(resolve(__dirname, c.path)) + ';'
                    )
                    .join('\n');

                const entries = comps
                    .map((c) =>
                        '  [' + JSON.stringify(c.name) +
                        ', { Component: _c' + c.name + '.default ?? _c' + c.name +
                        ', mode: ' + JSON.stringify(c.mode) + ' }],'
                    )
                    .join('\n');

                return {
                    contents:
                        imports +
                        '\nexport const COMPONENT_REGISTRY = new Map([\n' +
                        entries +
                        '\n]);',
                    loader: 'js',
                    resolveDir: __dirname,
                };
            });
        },
    };
}

if (watchMode) {
    const serverCtx = await esbuild.context(serverBuildOptions);
    const clientCtx = await esbuild.context(clientBuildOptions);
    await serverCtx.watch();
    await clientCtx.watch();
    console.error('[revelt] watching frontend files for changes...');
} else {
    const serverResult = await esbuild.build(serverBuildOptions);
    const clientResult = await esbuild.build(clientBuildOptions);
    if (serverResult.errors.length > 0 || clientResult.errors.length > 0) {
        process.exit(1);
    }
    console.error('[revelt] built → dist/render-server.cjs and dist/client/client.js');
}
