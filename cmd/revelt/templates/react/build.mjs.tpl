import * as esbuild from 'esbuild';
import { resolve, dirname, basename, extname } from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

const __dirname = dirname(fileURLToPath(import.meta.url));

const configPath = resolve(__dirname, '../revelt.json');
const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));

const componentDirName = config.component_dir ?? 'src/components';
const componentDir = resolve(__dirname, componentDirName);

// Derive root source path containing components (e.g., 'src' or '.')
const parentDir = dirname(componentDirName);

/** @typedef {'ssr' | 'hydrate' | 'client' | 'lazy-client'} ComponentMode */
/** @typedef {{ name: string, path: string, mode: ComponentMode }} ComponentEntry */

const MODE_ANNOTATION_RE = /@mode\s+(lazy-client|ssr|hydrate|client)/;
const MODE_ANNOTATION_SEARCH_LINES = 5;
const DEFAULT_MODE = /** @type {ComponentMode} */ ('hydrate');
const COMPONENT_EXTENSIONS = new Set(['.tsx', '.ts', '.jsx', '.js']);

/**
 * Reads the leading lines of a source file and extracts the declared
 * rendering mode from a `@mode <ssr|hydrate|client|lazy-client>` comment
 * annotation. Falls back to `'hydrate'` when no annotation is present.
 *
 * @param {string} filePath Absolute path to the component source file.
 * @returns {ComponentMode}
 */
function readModeAnnotation(filePath) {
    const source = fs.readFileSync(filePath, 'utf8');
    const lines = source.split('\n', MODE_ANNOTATION_SEARCH_LINES);
    for (const line of lines) {
        // Match lazy-client before client so the longer token wins.
        const match = line.match(MODE_ANNOTATION_RE);
        if (match) {
            return /** @type {ComponentMode} */ (match[1]);
        }
    }
    return DEFAULT_MODE;
}

/**
 * Discovers all component source files inside `componentDir`.
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
        .filter((file) => COMPONENT_EXTENSIONS.has(extname(file)))
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
const isDev = watchMode || process.env.NODE_ENV === 'development';

/**
 * Builds an `import * as _c<Name> from "<absPath>";` statement for a
 * component.
 *
 * @param {ComponentEntry} component
 * @returns {string}
 */
function toEagerImport(component) {
    const absPath = resolve(__dirname, component.path);
    return `import * as _c${component.name} from ${JSON.stringify(absPath)};`;
}

/**
 * Builds a `[name, { Component, mode }]` registry entry for an eagerly
 * imported component, unwrapping a default export when present.
 *
 * @param {ComponentEntry} component
 * @returns {string}
 */
function toEagerEntry(component) {
    return (
        `  [${JSON.stringify(component.name)}, { Component: _c${component.name}.default ?? _c${component.name}` +
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
 * esbuild plugin that generates the `revelt:registry` virtual module,
 * resolving `revelt:registry` imports to a synthesized module listing the
 * components relevant to `side`.
 *
 * @param {'server' | 'client'} side
 * @returns {esbuild.Plugin}
 */
function componentRegistryPlugin(side) {
    const registryPath = 'revelt:registry';

    return {
        name: 'revelt-component-registry',
        setup(build) {
            build.onResolve({ filter: /^revelt:registry$/ }, () => ({
                path: registryPath,
                namespace: 'revelt-registry',
            }));

            build.onLoad({ filter: /.*/, namespace: 'revelt-registry' }, () => {
                const all = discoverComponents();
                const comps = all.filter((c) =>
                    side === 'server'
                        ? c.mode === 'ssr' || c.mode === 'hydrate'
                        : c.mode === 'hydrate' || c.mode === 'client' || c.mode === 'lazy-client'
                );

                if (comps.length === 0) {
                    return {
                        contents: 'export const COMPONENT_REGISTRY = new Map();',
                        loader: 'js',
                        resolveDir: __dirname,
                        watchDirs: [componentDir],
                    };
                }

                const contents =
                    side === 'server'
                        ? renderRegistryModule(comps, [])
                        : renderRegistryModule(
                              comps.filter((c) => c.mode !== 'lazy-client'),
                              comps.filter((c) => c.mode === 'lazy-client')
                          );

                return {
                    contents,
                    loader: 'js',
                    resolveDir: __dirname,
                    watchFiles: comps.map((c) => resolve(__dirname, c.path)),
                    watchDirs: [componentDir],
                };
            });
        },
    };
}

/** @type {esbuild.BuildOptions} */
const serverBuildOptions = {
    entryPoints: ['render-server.js'],
    bundle: true,
    platform: 'node',
    format: 'cjs',
    outfile: 'dist/render-server.cjs',
    target: 'node18',
    jsx: 'transform',
    resolveExtensions: ['.tsx', '.ts', '.jsx', '.js', '.json'],
    alias: {
        '@': resolve(__dirname, parentDir),
    },
    sourcemap: watchMode ? 'inline' : false,
    logOverride: { 'ignored-bare-import': 'silent' },
    external: isDev ? ['react', 'react-dom', 'react-dom/server'] : [],
    plugins: [componentRegistryPlugin('server')],
};

// Client bundle: ESM format with code splitting enabled.
//
// esbuild emits one entry chunk per entrypoint plus additional shared chunks
// for any modules imported by more than one entry. Content hashes in filenames
// allow immutable Cache-Control headers on all chunk files while letting
// index.html stay short-lived (no-cache).
//
// `splitting: true` requires `format: 'esm'` — esbuild enforces this.
// `outdir` (not `outfile`) is also required when splitting is enabled because
// multiple output files will be produced.
/** @type {esbuild.BuildOptions} */
const clientBuildOptions = {
    entryPoints: ['client.tsx'],
    bundle: true,
    platform: 'browser',
    format: 'esm',
    splitting: true,
    outdir: 'dist/client',
    entryNames: watchMode ? '[name]' : '[name]-[hash]',
    chunkNames: watchMode ? 'chunks/[name]' : 'chunks/[name]-[hash]',
    target: 'es2020',
    jsx: 'transform',
    resolveExtensions: ['.tsx', '.ts', '.jsx', '.js', '.json'],
    alias: {
        '@': resolve(__dirname, parentDir),
    },
    sourcemap: watchMode ? 'inline' : false,
    logOverride: { 'ignored-bare-import': 'silent' },
    plugins: [componentRegistryPlugin('client')],
    minify: !watchMode,
    // Emit a metafile so injectAssets() can discover the hashed entry filename
    // without scanning the output directory.
    metafile: true,
};

/**
 * Compiles `app.css` with Tailwind v4 (via PostCSS) into
 * `dist/client/client.css`, when `app.css` exists. No-op otherwise.
 *
 * @returns {Promise<void>}
 */
async function buildCSS() {
    const cssInput = resolve(__dirname, parentDir, 'app.css');
    if (!fs.existsSync(cssInput)) return;

    try {
        const postcss = (await import('postcss')).default;
        const tailwindcss = (await import('@tailwindcss/postcss')).default;
        const cssContent = fs.readFileSync(cssInput, 'utf8');
        const result = await postcss([tailwindcss()]).process(cssContent, {
            from: cssInput,
            to: resolve(__dirname, 'dist/client/client.css'),
        });
        fs.mkdirSync(resolve(__dirname, 'dist/client'), { recursive: true });
        fs.writeFileSync(resolve(__dirname, 'dist/client/client.css'), result.css, 'utf8');
        console.error(`[revelt] built CSS with Tailwind v4 → dist/client/client.css`);
    } catch (err) {
        console.error('[revelt] failed to compile CSS:', err.message);
    }
}

/**
 * Determines the client entry-point output filenames for the current build.
 *
 * When a metafile is available (production builds), entry outputs are read
 * directly from `metafile.outputs`, avoiding a directory scan. In watch mode
 * (no metafile), falls back to scanning `dist/client` and matching the
 * `client[-hash].js` filename pattern, which is stable within a single watch
 * session.
 *
 * @param {string} clientDist Absolute path to the client build output dir.
 * @param {esbuild.Metafile | undefined} metafile esbuild metafile from the
 *   client build result, when available.
 * @returns {string[]} Relative filenames of entry client scripts.
 */
function findClientEntryFiles(clientDist, metafile) {
    if (metafile) {
        // Only target outputs originating from the client entry point.
        return Object.entries(metafile.outputs)
            .filter(([, meta]) => meta.entryPoint)
            .map(([outPath]) => outPath.replace(/^dist\/client\//, ''))
            .filter((file) => file.startsWith('client'));
    }

    const CLIENT_ENTRY_RE = /^client(-[a-zA-Z0-9]+)?\.js$/;
    return fs.readdirSync(clientDist).filter((file) => CLIENT_ENTRY_RE.test(file));
}

/**
 * Rewrites index.html to reference the current build outputs.
 *
 * For the client entry chunk we emit a `<script type="module">` tag — ESM
 * modules are deferred by the browser automatically, so no `defer` attribute
 * is needed. We also emit a `<link rel="modulepreload">` for the entry so the
 * browser can begin fetching it in parallel with HTML parsing rather than
 * waiting until the parser reaches the `<script>` tag at the end of `<body>`.
 *
 * Shared chunks (under chunks/) do not need explicit tags; the browser fetches
 * them as dynamic imports triggered by the entry module.
 *
 * @param {esbuild.Metafile | undefined} metafile esbuild metafile from the
 *   client build result. When present, the entry filename is read from it
 *   directly (avoids scanning the directory for the hashed name).
 * @returns {void}
 */
function injectAssets(metafile) {
    const staticPrefix = config.static_prefix ?? '/static/';
    const clientDist = resolve(__dirname, 'dist/client');
    if (!fs.existsSync(clientDist)) return;

    const entryFiles = findClientEntryFiles(clientDist, metafile);
    const modulePreloads = entryFiles.map(
        (file) => `<link rel="modulepreload" href="${staticPrefix}${file}">`
    );
    const moduleScripts = entryFiles.map(
        (file) => `<script type="module" src="${staticPrefix}${file}"></script>`
    );

    // Collect CSS files (Tailwind output, etc.).
    const styles = fs
        .readdirSync(clientDist)
        .filter((file) => file.endsWith('.css'))
        .map((file) => `<link rel="stylesheet" href="${staticPrefix}${file}">`);

    const templatePath = resolve(__dirname, 'index.html');
    if (!fs.existsSync(templatePath)) return;

    let html = fs.readFileSync(templatePath, 'utf8');

    // Strip previously injected tags to prevent duplicates across rebuilds.
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

if (watchMode) {
    const serverCtx = await esbuild.context(serverBuildOptions);

    // In watch mode we forego metafile-based asset injection because esbuild's
    // onEnd callback does not receive the result directly. We fall back to the
    // directory scan path inside injectAssets(), which is correct for watch
    // mode since hashes are stable within a single watch session.
    const clientCtx = await esbuild.context({
        ...clientBuildOptions,
        plugins: [
            ...clientBuildOptions.plugins,
            {
                name: 'html-inject-plugin',
                setup(build) {
                    build.onEnd(async () => {
                        await buildCSS();
                        injectAssets(undefined);
                    });
                },
            },
        ],
    });
    await serverCtx.watch();
    await clientCtx.watch();
    console.error('[revelt] watching frontend files for changes...');
} else {
    const serverResult = await esbuild.build(serverBuildOptions);
    const clientResult = await esbuild.build(clientBuildOptions);
    if (serverResult.errors.length > 0 || clientResult.errors.length > 0) {
        process.exit(1);
    }
    await buildCSS();
    injectAssets(clientResult.metafile);
    console.error('[revelt] built → dist/render-server.cjs and dist/client/');
}
