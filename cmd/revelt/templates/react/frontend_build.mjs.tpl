import * as esbuild from 'esbuild';
import { resolve, dirname, basename, extname } from 'node:path';
import { fileURLToPath } from 'node:url';
import fs from 'node:fs';

const __dirname = dirname(fileURLToPath(import.meta.url));

const configPath = resolve(__dirname, '../revelt.json');
const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));

const componentDirName = config.component_dir ?? 'src/components';
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
        // Match lazy-client before client so the longer token wins.
        const m = line.match(/@mode\s+(lazy-client|ssr|hydrate|client)/);
        if (m) {
            return /** @type {ComponentMode} */ (m[1]);
        }
    }
    return 'hydrate';
}

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

const initialComponents = discoverComponents();
console.error(
    `[revelt] discovered ${initialComponents.length} component(s): ` +
    initialComponents.map((c) => `${c.name}(${c.mode})`).join(', ')
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
    jsx: 'transform',
    resolveExtensions: ['.tsx', '.ts', '.jsx', '.js', '.json'],
    alias: {
        '@': resolve(__dirname, 'src'),
    },
    sourcemap: watchMode ? 'inline' : false,
    logOverride: { 'ignored-bare-import': 'silent' },
    external: ['react', 'react-dom', 'react-dom/server'],
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
    // Entry chunks: client-<hash>.js
    entryNames: '[name]-[hash]',
    // Shared chunks go into a subdirectory to keep dist/client/ tidy.
    chunkNames: 'chunks/[name]-[hash]',
    target: 'es2020',
    jsx: 'transform',
    resolveExtensions: ['.tsx', '.ts', '.jsx', '.js', '.json'],
    alias: {
        '@': resolve(__dirname, 'src'),
    },
    sourcemap: watchMode ? 'inline' : false,
    logOverride: { 'ignored-bare-import': 'silent' },
    plugins: [componentRegistryPlugin('client')],
    minify: !watchMode,
    // Emit a metafile so injectAssets() can discover the hashed entry filename
    // without scanning the output directory.
    metafile: true,
};

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

                if (side === 'server') {
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
                        watchFiles: comps.map((c) => resolve(__dirname, c.path)),
                        watchDirs: [componentDir],
                    };
                }

                // Client side: Dynamic route splitting for lazy components
                const eager = comps.filter((c) => c.mode !== 'lazy-client');
                const lazy = comps.filter((c) => c.mode === 'lazy-client');

                const eagerImports = eager
                    .map((c) =>
                        'import * as _c' + c.name +
                        ' from ' + JSON.stringify(resolve(__dirname, c.path)) + ';'
                    )
                    .join('\n');

                const eagerEntries = eager
                    .map((c) =>
                        '  [' + JSON.stringify(c.name) +
                        ', { Component: _c' + c.name + '.default ?? _c' + c.name +
                        ', mode: ' + JSON.stringify(c.mode) + ' }],'
                    )
                    .join('\n');

                const lazyEntries = lazy
                    .map((c) =>
                        '  [' + JSON.stringify(c.name) +
                        ', { load: () => import(' + JSON.stringify(resolve(__dirname, c.path)) + ')' +
                        '.then((m) => m.default ?? m)' +
                        ', mode: ' + JSON.stringify(c.mode) + ' }],'
                    )
                    .join('\n');

                const allEntries = [eagerEntries, lazyEntries].filter(Boolean).join('\n');

                return {
                    contents:
                        eagerImports +
                        '\nexport const COMPONENT_REGISTRY = new Map([\n' +
                        allEntries +
                        '\n]);',
                    loader: 'js',
                    resolveDir: __dirname,
                    watchFiles: comps.map((c) => resolve(__dirname, c.path)),
                    watchDirs: [componentDir],
                };
            });
        },
    };
}

async function buildCSS() {
    const cssInput = resolve(__dirname, 'src/app.css');
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
        console.error('[revelt] built CSS with Tailwind v4 → dist/client/client.css');
    } catch (err) {
        console.error('[revelt] failed to compile CSS:', err.message);
    }
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
 * @param {esbuild.Metafile | undefined} metafile  esbuild metafile from the
 *   client build result. When present, the entry filename is read from it
 *   directly (avoids scanning the directory for the hashed name).
 */
function injectAssets(metafile) {
    const staticPrefix = config.static_prefix ?? '/static/';
    const moduleScripts = [];
    const modulePreloads = [];
    const styles = [];

    const clientDist = resolve(__dirname, 'dist/client');
    if (!fs.existsSync(clientDist)) return;

    if (metafile) {
        // When using metafile, only target outputs originating from the client entry point
        for (const [outPath, meta] of Object.entries(metafile.outputs)) {
            if (!meta.entryPoint) continue;
            const file = outPath.replace(/^dist\/client\//, '');
            if (file.startsWith('client')) {
                modulePreloads.push(
                    `<link rel="modulepreload" href="${staticPrefix}${file}">`
                );
                moduleScripts.push(
                    `<script type="module" src="${staticPrefix}${file}"></script>`
                );
            }
        }
    } else {
        // Fallback for watch mode: scan the directory but strictly match the main entry file pattern
        const files = fs.readdirSync(clientDist);
        for (const file of files) {
            const isEntryFile = file === 'client.js' || /^client-[a-zA-Z0-9]+\.js$/.test(file);
            if (isEntryFile) {
                modulePreloads.push(
                    `<link rel="modulepreload" href="${staticPrefix}${file}">`
                );
                moduleScripts.push(
                    `<script type="module" src="${staticPrefix}${file}"></script>`
                );
            }
        }
    }

    // Collect CSS files (Tailwind output, etc.).
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
