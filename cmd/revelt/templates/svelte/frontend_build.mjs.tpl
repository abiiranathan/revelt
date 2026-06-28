import { build } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import { resolve, dirname, basename, extname } from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

const __dirname = dirname(fileURLToPath(import.meta.url));

const configPath = resolve(__dirname, '../revelt.json');
const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));

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
 * Discovers all Svelte component files inside `componentDir`.
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

const initialComponents = discoverComponents();
console.error(
    `[revelt] discovered ${initialComponents.length} component(s): ` +
    initialComponents.map((c) => `${c.name}(${c.mode})`).join(', ')
);

const watchMode = process.argv.includes('--watch');

/**
 * Vite plugin that provides a virtual `revelt:registry` module whose contents
 * are regenerated on every load from the live component list.
 *
 * @param {'server' | 'client'} side
 * @returns {import('vite').Plugin}
 */
function componentRegistryPlugin(side) {
    const virtualId = 'revelt:registry';
    const resolvedId = '\0revelt:registry';

    return {
        name: 'revelt-component-registry',

        resolveId(id) {
            if (id === virtualId) return resolvedId;
        },

        load(id) {
            if (id !== resolvedId) return;

            const all = discoverComponents();
            const comps = all.filter((c) =>
                side === 'server'
                    ? c.mode === 'ssr' || c.mode === 'hydrate'
                    : c.mode === 'hydrate' || c.mode === 'client'
            );

            for (const c of comps) {
                this.addWatchFile(resolve(__dirname, c.path));
            }
            this.addWatchFile(componentDir);

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

/**
 * Rewrites index.html to reference the current build outputs.
 *
 * Vite's build manifest (`generate: 'manifest'`) maps logical input names to
 * hashed output filenames. We use it to find the entry chunk rather than
 * scanning the output directory, which avoids accidentally picking up shared
 * chunks or asset files.
 *
 * The entry is injected as `<script type="module">` — browsers defer ESM
 * automatically, so no `defer` attribute is required. A `<link rel="modulepreload">`
 * is also injected so the browser fetches the entry chunk in parallel with HTML
 * parsing rather than waiting until the `<script>` tag is encountered.
 */
function injectAssets() {
    const staticPrefix = config.static_prefix ?? '/static/';
    const moduleScripts = [];
    const modulePreloads = [];
    const styles = [];

    const clientDist = resolve(__dirname, 'dist/client');
    if (!fs.existsSync(clientDist)) return;

    // Read Vite's manifest to identify the entry chunk by its `isEntry` flag.
    // The manifest is emitted to dist/client/.vite/manifest.json.
    const manifestPath = resolve(clientDist, '.vite/manifest.json');
    if (fs.existsSync(manifestPath)) {
        const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
        for (const entry of Object.values(manifest)) {
            if (!entry.isEntry) continue;
            modulePreloads.push(
                `<link rel="modulepreload" href="${staticPrefix}${entry.file}">`
            );
            moduleScripts.push(
                `<script type="module" src="${staticPrefix}${entry.file}"></script>`
            );
        }
    } else {
        // Fallback: scan top-level .js files (watch mode / manifest disabled).
        const files = fs.readdirSync(clientDist);
        for (const file of files) {
            if (file.endsWith('.js')) {
                modulePreloads.push(
                    `<link rel="modulepreload" href="${staticPrefix}${file}">`
                );
                moduleScripts.push(
                    `<script type="module" src="${staticPrefix}${file}"></script>`
                );
            }
        }
    }

    // Collect CSS outputs.
    const files = fs.readdirSync(clientDist);
    for (const file of files) {
        if (file.endsWith('.css')) {
            styles.push(`<link rel="stylesheet" href="${staticPrefix}${file}">`);
        }
    }

    const templatePath = resolve(__dirname, 'index.html');
    if (!fs.existsSync(templatePath)) return;

    let html = fs.readFileSync(templatePath, 'utf8');

    // Strip previously injected tags to prevent duplicates across rebuilds.
    html = html.replace(/<link rel="modulepreload"[^>]+>/g, '');
    html = html.replace(/<link rel="stylesheet" href="[^"]+">/g, '');
    html = html.replace(/<script type="module"[^>]*><\/script>/g, '');
    // Remove legacy defer tags from projects built before this change.
    html = html.replace(/<script src="[^"]+" defer><\/script>/g, '');

    if (modulePreloads.length > 0) {
        html = html.replace(
            '</head>',
            '    ' + modulePreloads.join('\n    ') + '\n</head>'
        );
    }
    if (styles.length > 0) {
        html = html.replace(
            '</head>',
            '    ' + styles.join('\n    ') + '\n</head>'
        );
    }
    if (moduleScripts.length > 0) {
        html = html.replace(
            '</body>',
            '    ' + moduleScripts.join('\n    ') + '\n</body>'
        );
    }

    const outPath = resolve(clientDist, 'index.html');
    fs.mkdirSync(dirname(outPath), { recursive: true });
    fs.writeFileSync(outPath, html, 'utf8');

    console.error(`[revelt] injected assets into ${outPath}`);
}

function htmlPlugin() {
    return {
        name: 'html-inject-plugin',
        closeBundle() {
            injectAssets();
        },
    };
}

const clientPlugins = [
    svelte(),
    componentRegistryPlugin('client'),
    htmlPlugin(),
];

const serverPlugins = [
    svelte(),
    componentRegistryPlugin('server'),
];

const appCssPath = resolve(__dirname, 'src/app.css');
if (fs.existsSync(appCssPath)) {
    try {
        const tailwind = (await import('@tailwindcss/vite')).default;
        clientPlugins.push(tailwind());
        serverPlugins.push(tailwind());
    } catch (err) {
        console.error('[revelt] @tailwindcss/vite not installed, skipping tailwind compilation');
    }
}

/** @type {import('vite').UserConfig} */
const serverConfig = {
    plugins: serverPlugins,
    resolve: {
        alias: {
            '@': resolve(__dirname, 'src'),
        },
    },
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

// Client bundle: ESM format with Rollup code splitting.
//
// Vite/Rollup splits code at dynamic import() boundaries. Setting
// `format: 'es'` and omitting a fixed `entryFileNames` lets Rollup add
// content hashes so every chunk gets an immutable filename. The manifest
// (`generate: 'manifest'`) lets injectAssets() look up the hashed entry
// filename without directory scanning.
//
// Shared chunks are emitted automatically by Rollup whenever two or more
// entry/dynamic-import chunks share a common dependency. They land alongside
// the entry chunk in dist/client/ and are fetched by the browser as needed
// when the entry module runs.
/** @type {import('vite').UserConfig} */
const clientConfig = {
    plugins: clientPlugins,
    resolve: {
        alias: {
            '@': resolve(__dirname, 'src'),
        },
    },
    build: {
        minify: !watchMode,
        sourcemap: watchMode,
        outDir: 'dist/client',
        emptyOutDir: false,
        // Emit the Vite manifest so injectAssets() can resolve hashed filenames.
        manifest: true,
        rollupOptions: {
            input: 'client.js',
            output: {
                // ESM is required for native browser code splitting.
                format: 'es',
                // Content-hash entry and chunk filenames for immutable caching.
                entryFileNames: '[name]-[hash].js',
                chunkFileNames: 'chunks/[name]-[hash].js',
                assetFileNames: '[name]-[hash].[ext]',
            },
        },
    },
};

if (watchMode) {
    const serverWatcher = await build({
        ...serverConfig,
        build: { ...serverConfig.build, watch: {} },
    });

    const clientWatcher = await build({
        ...clientConfig,
        build: { ...clientConfig.build, watch: {} },
    });

    console.error('[revelt] watching frontend files for changes...');

    process.on('SIGINT', async () => {
        await serverWatcher.close();
        await clientWatcher.close();
        process.exit(0);
    });
} else {
    await build(serverConfig);
    await build(clientConfig);
    console.error('[revelt] built → dist/render-server.cjs and dist/client/');
}
