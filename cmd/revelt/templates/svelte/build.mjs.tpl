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

// Derive root source path containing components (e.g., 'src' or '.')
const parentDir = dirname(componentDirName);

/** @typedef {'ssr' | 'hydrate' | 'client' | 'lazy-client'} ComponentMode */
/** @typedef {{ name: string, path: string, mode: ComponentMode }} ComponentEntry */

const MODE_ANNOTATION_RE = /@mode\s+(lazy-client|ssr|hydrate|client)/;
const MODE_ANNOTATION_SEARCH_LINES = 5;
const DEFAULT_MODE = /** @type {ComponentMode} */ ('hydrate');

/**
 * Reads the leading lines of a source file and extracts the declared
 * rendering mode from a `@mode <ssr|hydrate|client|lazy-client>` annotation.
 * Falls back to `'hydrate'` when no annotation is present.
 *
 * @param {string} filePath Absolute path to the component source file.
 * @returns {ComponentMode}
 */
function readModeAnnotation(filePath) {
    try {
        const source = fs.readFileSync(filePath, 'utf8');
        const lines = source.split('\n', MODE_ANNOTATION_SEARCH_LINES);
        for (const line of lines) {
            const match = line.match(MODE_ANNOTATION_RE);
            if (match) {
                return /** @type {ComponentMode} */ (match[1]);
            }
        }
    } catch (err) {
        // Fall back to 'hydrate' if the file is temporarily unreadable
    }
    return DEFAULT_MODE;
}

/**
 * Discovers all Svelte component files inside `componentDir`.
 *
 * @returns {ComponentEntry[]}
 */
function discoverComponents() {
    if (!fs.existsSync(componentDir)) {
        console.error(`[revelt] component directory not found: ${componentDir}`);
        return [];
    }

    return fs
        .readdirSync(componentDir, { recursive: true })
        .filter((file) => extname(file) === '.svelte')
        .map((file) => {
            const absPath = resolve(componentDir, file);
            const name = basename(file, extname(file));
            const mode = readModeAnnotation(absPath);
            return { name, path: `./${componentDirName}/${file}`, mode };
        });
}

/**
 * Computes a stable fingerprint for the current component set, used to
 * detect meaningful structural changes (add/remove/rename/mode change).
 *
 * @param {string} [fallback] Value to return if discovery fails.
 * @returns {string}
 */
function getComponentsFingerprint(fallback = '') {
    try {
        return discoverComponents()
            .map((c) => `${c.name}:${c.path}:${c.mode}`)
            .sort()
            .join('|');
    } catch (err) {
        return fallback;
    }
}

// Editor saves (Vim, VSCode, etc.) commonly perform an atomic write: write to
// a temp file, then rename it into place. fs.watch's 'recursive' mode emits
// multiple raw events for that single logical save (e.g. rename + change),
// and can transiently observe the directory mid-write. Without debouncing,
// each of those events re-evaluates the fingerprint and — if the scan
// happens to race the rename — can spuriously believe the component set
// changed, triggering fs.utimesSync on client.ts/render-server.js, which in
// turn makes Rollup's watcher rebuild everything even though nothing
// meaningful changed. Coalescing events into a single fingerprint check
// after the filesystem has settled eliminates the false positives.
const COMPONENT_WATCH_DEBOUNCE_MS = 150;

/** Files touched to force a rebuild when the component set changes. */
const REBUILD_TRIGGER_FILES = ['render-server.js', 'client.ts'];

/**
 * Touches `REBUILD_TRIGGER_FILES` with the current time to force Rollup's
 * watcher to rebuild, ignoring any individual file that no longer exists
 * or can't be touched.
 *
 * @returns {void}
 */
function touchRebuildTriggers() {
    const now = new Date();
    for (const relPath of REBUILD_TRIGGER_FILES) {
        const absPath = resolve(__dirname, relPath);
        if (!fs.existsSync(absPath)) continue;
        try {
            fs.utimesSync(absPath, now, now);
        } catch (err) {
            // ignore error
        }
    }
}

/**
 * Watches `componentDir` for structural changes and touches the rebuild
 * trigger files when the debounced fingerprint differs from the last
 * observed one.
 *
 * @returns {void}
 */
function setupComponentDirWatcher() {
    if (!fs.existsSync(componentDir)) return;

    let lastFingerprint = getComponentsFingerprint();
    /** @type {NodeJS.Timeout | null} */
    let debounceTimer = null;

    const onChange = () => {
        if (debounceTimer) clearTimeout(debounceTimer);
        debounceTimer = setTimeout(() => {
            debounceTimer = null;

            const currentFingerprint = getComponentsFingerprint(lastFingerprint);
            if (currentFingerprint === lastFingerprint) return;

            lastFingerprint = currentFingerprint;
            console.error('[revelt] component directory structure changed; triggering rebuild...');
            touchRebuildTriggers();
        }, COMPONENT_WATCH_DEBOUNCE_MS);
    };

    try {
        fs.watch(componentDir, { recursive: true }, onChange);
    } catch (err) {
        fs.watch(componentDir, onChange);
    }
}

const initialComponents = discoverComponents();
console.error(
    `[revelt] discovered ${initialComponents.length} component(s): ` +
    initialComponents.map((c) => `${c.name}(${c.mode})`).join(', ')
);

const watchMode = process.argv.includes('--watch');
const isDev = watchMode || process.env.NODE_ENV === 'development';

/**
 * Builds an `import _c<Name> from "<absPath>";` statement for a component.
 *
 * @param {ComponentEntry} component
 * @returns {string}
 */
function toEagerImport(component) {
    const absPath = resolve(__dirname, component.path);
    return `import _c${component.name} from ${JSON.stringify(absPath)};`;
}

/**
 * Builds a `[name, { Component, mode }]` registry entry for an eagerly
 * imported component.
 *
 * @param {ComponentEntry} component
 * @returns {string}
 */
function toEagerEntry(component) {
    return (
        `  [${JSON.stringify(component.name)}, { Component: _c${component.name}` +
        `, mode: ${JSON.stringify(component.mode)} }],`
    );
}

/**
 * Builds a `[name, { load, mode }]` registry entry for a lazily loaded
 * (dynamically imported) component.
 *
 * @param {ComponentEntry} component
 * @returns {string}
 */
function toLazyEntry(component) {
    const absPath = resolve(__dirname, component.path);
    return (
        `  [${JSON.stringify(component.name)}, { load: () => import(${JSON.stringify(absPath)})` +
        `.then((m) => m.default ?? m), mode: ${JSON.stringify(component.mode)} }],`
    );
}

/**
 * Renders the `revelt:registry` virtual module source from eager and lazy
 * component sets.
 *
 * @param {ComponentEntry[]} eager Components imported statically.
 * @param {ComponentEntry[]} lazy Components imported via dynamic `import()`.
 * @returns {string}
 */
function renderRegistryModule(eager, lazy) {
    if (eager.length === 0 && lazy.length === 0) {
        return 'export const COMPONENT_REGISTRY = new Map();';
    }

    const imports = eager.map(toEagerImport).join('\n');
    const entries = [...eager.map(toEagerEntry), ...lazy.map(toLazyEntry)].join('\n');

    return `${imports}\nexport const COMPONENT_REGISTRY = new Map([\n${entries}\n]);`;
}

/**
 * Vite plugin that generates the `revelt:registry` virtual module.
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

            if (side === 'server') {
                const ssrOrHydrate = all.filter((c) => c.mode === 'ssr' || c.mode === 'hydrate');
                return renderRegistryModule(ssrOrHydrate, []);
            }

            // Client side: hydrate/client components load eagerly,
            // lazy-client components load on demand.
            const eager = all.filter((c) => c.mode === 'hydrate' || c.mode === 'client');
            const lazy = all.filter((c) => c.mode === 'lazy-client');
            return renderRegistryModule(eager, lazy);
        },
    };
}

/**
 * Reads Vite's manifest (when present) to find entry-point client bundle
 * files, or falls back to scanning `dist/client` for `client[-hash].js`.
 *
 * @param {string} clientDist Absolute path to the client build output dir.
 * @returns {string[]} Relative filenames of entry client scripts.
 */
function findClientEntryFiles(clientDist) {
    const manifestPath = resolve(clientDist, '.vite/manifest.json');
    if (fs.existsSync(manifestPath)) {
        const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
        return Object.values(manifest)
            .filter((entry) => entry.isEntry && entry.file.startsWith('client'))
            .map((entry) => entry.file);
    }

    const CLIENT_ENTRY_RE = /^client-[a-zA-Z0-9]+\.js$/;
    return fs
        .readdirSync(clientDist)
        .filter((file) => file === 'client.js' || CLIENT_ENTRY_RE.test(file));
}

/**
 * Injects generated `<link>`/`<script>` tags for the built client assets
 * into `index.html`, writing the result into `dist/client/index.html`.
 *
 * @returns {void}
 */
function injectAssets() {
    const staticPrefix = config.static_prefix ?? '/static/';
    const clientDist = resolve(__dirname, 'dist/client');
    if (!fs.existsSync(clientDist)) return;

    const entryFiles = findClientEntryFiles(clientDist);
    const modulePreloads = entryFiles.map(
        (file) => `<link rel="modulepreload" href="${staticPrefix}${file}">`
    );
    const moduleScripts = entryFiles.map(
        (file) => `<script type="module" src="${staticPrefix}${file}"></script>`
    );

    const styles = fs
        .readdirSync(clientDist)
        .filter((file) => file.endsWith('.css'))
        .map((file) => `<link rel="stylesheet" href="${staticPrefix}${file}">`);

    const templatePath = resolve(__dirname, 'index.html');
    if (!fs.existsSync(templatePath)) return;

    let html = fs.readFileSync(templatePath, 'utf8');

    html = html.replace(/<link rel="modulepreload"[^>]+>/g, '');
    html = html.replace(/<link rel="stylesheet" href="[^"]+">/g, '');
    html = html.replace(/<script type="module"[^>]*><\/script>/g, '');
    html = html.replace(/<script src="[^"]+" defer><\/script>/g, '');

    if (modulePreloads.length > 0) {
        html = html.replace('</head>', `    ${modulePreloads.join('\n    ')}\n</head>`);
    }
    if (styles.length > 0) {
        html = html.replace('</head>', `    ${styles.join('\n    ')}\n</head>`);
    }
    if (moduleScripts.length > 0) {
        html = html.replace('</body>', `    ${moduleScripts.join('\n    ')}\n</body>`);
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

const appCssPath = resolve(__dirname, parentDir, 'app.css');
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
        alias: { '@': resolve(__dirname, parentDir) },
    },
    ssr: {
        noExternal: true,
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
        alias: { '@': resolve(__dirname, parentDir) },
    },
    build: {
        minify: !watchMode,
        sourcemap: watchMode,
        outDir: 'dist/client',
        emptyOutDir: false,
        manifest: true,
        watch: watchMode ? {
            exclude: ['node_modules/**', 'dist/**'],
            buildDelay: 300,
        } : null,
        rollupOptions: {
            input: 'client.ts',
            output: {
                format: 'es',
                entryFileNames: watchMode ? '[name].js' : '[name]-[hash].js',
                chunkFileNames: watchMode ? 'chunks/[name].js' : 'chunks/[name]-[hash].js',
                assetFileNames: watchMode ? '[name].[ext]' : '[name]-[hash].[ext]',
            },
        },
    },
};

if (watchMode) {
    setupComponentDirWatcher();

    const serverWatcher = await build({
        ...serverConfig,
        build: {
            ...serverConfig.build,
            watch: { exclude: ['node_modules/**', resolve(__dirname, 'dist/**')] },
        },
    });
    const clientWatcher = await build({
        ...clientConfig,
        build: {
            ...clientConfig.build,
            watch: { exclude: ['node_modules/**', resolve(__dirname, 'dist/**')] },
        },
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
