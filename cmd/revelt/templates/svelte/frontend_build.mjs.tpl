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

// Log the initial discovery once at startup for orientation; the plugin
// re-discovers on every load call so this count may drift during watch mode.
const initialComponents = discoverComponents();
console.error(
    `[revelt] discovered ${initialComponents.length} component(s): ` +
    initialComponents.map((c) => `${c.name}(${c.mode})`).join(', ')
);

const watchMode = process.argv.includes('--watch');

/**
 * Vite plugin that provides a virtual `revelt:registry` module whose contents
 * are regenerated on every load from the live component list. Component
 * discovery is deferred to `load()` so that watch-mode rebuilds always reflect
 * the current state of the component directory.
 *
 * `this.addWatchFile()` registers each discovered component and the directory
 * itself as tracked dependencies. Rollup/Vite then invalidates this virtual
 * module whenever any of those paths change, causing `load()` to re-run and
 * producing an up-to-date registry without restarting the process.
 *
 * @param {'server' | 'client'} side
 *   Controls which component modes are included in the registry:
 *   - `'server'`: `ssr` and `hydrate` components.
 *   - `'client'`: `hydrate` and `client` components.
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

            // Re-discover on every load call so additions, deletions, and
            // @mode annotation changes are reflected without restarting.
            const all = discoverComponents();
            const comps = all.filter((c) =>
                side === 'server'
                    ? c.mode === 'ssr' || c.mode === 'hydrate'
                    : c.mode === 'hydrate' || c.mode === 'client'
            );

            // Register each currently-known component as a watched dependency
            // so Rollup invalidates this virtual module on content edits and
            // @mode annotation changes.
            for (const c of comps) {
                this.addWatchFile(resolve(__dirname, c.path));
            }
            // Watch the directory itself to catch additions and deletions that
            // would not appear in the previous file list.
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

// Auto-inject styles & scripts into index.html based on actual dist/client/ files.
function injectAssets() {
    const staticPrefix = config.static_prefix ?? '/static/';
    const scripts = [];
    const styles = [];

    const clientDist = resolve(__dirname, 'dist/client');
    if (fs.existsSync(clientDist)) {
        const files = fs.readdirSync(clientDist);
        for (const file of files) {
            if (file.endsWith('.js')) {
                scripts.push(`<script src="${staticPrefix}${file}" defer></script>`);
            } else if (file.endsWith('.css')) {
                styles.push(`<link rel="stylesheet" href="${staticPrefix}${file}">`);
            }
        }
    }

    const templatePath = resolve(__dirname, 'index.html');
    if (!fs.existsSync(templatePath)) return;

    let html = fs.readFileSync(templatePath, 'utf8');

    // Remove any previously injected tags to avoid duplicates across rebuilds.
    html = html.replace(/<link rel="stylesheet" href="[^"]+">/g, '');
    html = html.replace(/<script src="[^"]+" defer><\/script>/g, '');

    if (styles.length > 0) {
        html = html.replace('</head>', '    ' + styles.join('\n    ') + '\n</head>');
    }
    if (scripts.length > 0) {
        html = html.replace('</body>', '    ' + scripts.join('\n    ') + '\n</body>');
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
        rollupOptions: {
            input: 'client.js',
            output: {
                format: 'iife',
                entryFileNames: 'client.js',
                assetFileNames: '[name].[ext]',
            },
        },
    },
};

if (watchMode) {
    // build() with watch: {} returns a RollupWatcher. It must be held in a
    // variable — if the reference is dropped the watcher is garbage-collected
    // and the process exits immediately after the first build completes.
    const serverWatcher = await build({
        ...serverConfig,
        build: { ...serverConfig.build, watch: {} },
    });

    const clientWatcher = await build({
        ...clientConfig,
        build: { ...clientConfig.build, watch: {} },
    });

    console.error('[revelt] watching frontend files for changes...');

    // Close both watchers on Ctrl-C so Rollup can flush any pending writes
    // before the process exits, mirroring the esbuild ctx.dispose() pattern.
    process.on('SIGINT', async () => {
        await serverWatcher.close();
        await clientWatcher.close();
        process.exit(0);
    });
} else {
    await build(serverConfig);
    await build(clientConfig);
    console.error('[revelt] built → dist/render-server.cjs and dist/client/client.js');
}
