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

/**
 * Reads the leading lines of a source file and extracts the declared
 * rendering mode from a `@mode <ssr|hydrate|client|lazy-client>` annotation.
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
        .readdirSync(componentDir, { recursive: true })
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
 * Vite plugin that generates the `revelt:registry` virtual module.
 *
 * Component modes per side:
 *
 *   server  — 'ssr' and 'hydrate' only; static imports.
 *   client  — 'hydrate' and 'client' as static imports;
 *             'lazy-client' as a `load` thunk using dynamic import().
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
                const comps = all.filter(
                    (c) => c.mode === 'ssr' || c.mode === 'hydrate'
                );

                for (const c of comps) this.addWatchFile(resolve(__dirname, c.path));
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
            }

            // Client side.
            const eager = all.filter(
                (c) => c.mode === 'hydrate' || c.mode === 'client'
            );
            const lazy = all.filter((c) => c.mode === 'lazy-client');

            for (const c of [...eager, ...lazy]) {
                this.addWatchFile(resolve(__dirname, c.path));
            }
            this.addWatchFile(componentDir);

            if (eager.length === 0 && lazy.length === 0) {
                return 'export const COMPONENT_REGISTRY = new Map();';
            }

            const eagerImports = eager
                .map((c) =>
                    'import _c' + c.name +
                    ' from ' + JSON.stringify(resolve(__dirname, c.path)) + ';'
                )
                .join('\n');

            const eagerEntries = eager
                .map((c) =>
                    '  [' + JSON.stringify(c.name) +
                    ', { Component: _c' + c.name +
                    ', mode: ' + JSON.stringify(c.mode) + ' }],'
                )
                .join('\n');

            // Lazy entries: dynamic import() so Rollup splits into a separate
            // chunk that is fetched only when load() is first called.
            const lazyEntries = lazy
                .map((c) =>
                    '  [' + JSON.stringify(c.name) +
                    ', { load: () => import(' + JSON.stringify(resolve(__dirname, c.path)) + ')' +
                    '.then((m) => m.default ?? m)' +
                    ', mode: ' + JSON.stringify(c.mode) + ' }],'
                )
                .join('\n');

            const allEntries = [eagerEntries, lazyEntries]
                .filter(Boolean)
                .join('\n');

            return (
                eagerImports +
                '\nexport const COMPONENT_REGISTRY = new Map([\n' +
                allEntries +
                '\n]);'
            );
        },
    };
}

function injectAssets() {
    const staticPrefix = config.static_prefix ?? '/static/';
    const moduleScripts = [];
    const modulePreloads = [];
    const styles = [];

    const clientDist = resolve(__dirname, 'dist/client');
    if (!fs.existsSync(clientDist)) return;

    const manifestPath = resolve(clientDist, '.vite/manifest.json');
    if (fs.existsSync(manifestPath)) {
        const manifest = JSON.parse(fs.readFileSync(manifestPath, 'utf8'));
        for (const entry of Object.values(manifest)) {
            if (!entry.isEntry) continue;
            
            // Only inject the main entry file (starting with 'client'), skipping dynamic split chunks
            const file = entry.file;
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
        const files = fs.readdirSync(clientDist);
        for (const file of files) {
            // Match main client entry file (e.g. client.js or client-hash.js), ignoring lazy route chunks
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

    const files = fs.readdirSync(clientDist);
    for (const file of files) {
        if (file.endsWith('.css')) {
            styles.push(`<link rel="stylesheet" href="${staticPrefix}${file}">`);
        }
    }

    const templatePath = resolve(__dirname, 'index.html');
    if (!fs.existsSync(templatePath)) return;

    let html = fs.readFileSync(templatePath, 'utf8');

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
        alias: { '@': resolve(__dirname, parentDir) },
    },
    build: {
        minify: !watchMode,
        sourcemap: watchMode,
        outDir: 'dist/client',
        emptyOutDir: false,
        manifest: true,
        rollupOptions: {
            input: 'client.ts',
            output: {
                format: 'es',
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
