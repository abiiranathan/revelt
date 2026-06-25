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
        const m = line.match(/@mode\s+(ssr|hydrate|client)/);
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
    // Force Classic React Runtime to ensure strict single-instance matching
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

/** @type {esbuild.BuildOptions} */
const clientBuildOptions = {
    entryPoints: ['client.tsx'],
    bundle: true,
    platform: 'browser',
    format: 'iife',
    outfile: 'dist/client/client.js',
    target: 'es2020',
    // Force Classic React Runtime to resolve all rendering strictly to root React
    jsx: 'transform',
    resolveExtensions: ['.tsx', '.ts', '.jsx', '.js', '.json'],
    alias: {
        '@': resolve(__dirname, 'src'),
        'react': resolve(__dirname, 'node_modules/react'),
        'react-dom': resolve(__dirname, 'node_modules/react-dom'),
    },
    sourcemap: watchMode ? 'inline' : false,
    logOverride: { 'ignored-bare-import': 'silent' },
    plugins: [componentRegistryPlugin('client')],
    minify: !watchMode,
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
                // Re-discover on every build so additions, deletions, and
                // @mode annotation changes are reflected without restarting.
                const all = discoverComponents();
                const comps = all.filter((c) =>
                    side === 'server'
                        ? c.mode === 'ssr' || c.mode === 'hydrate'
                        : c.mode === 'hydrate' || c.mode === 'client'
                );

                if (comps.length === 0) {
                    return {
                        contents: 'export const COMPONENT_REGISTRY = new Map();',
                        loader: 'js',
                        resolveDir: __dirname,
                        watchDirs: [componentDir],
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

if (watchMode) {
    const serverCtx = await esbuild.context(serverBuildOptions);
    const clientCtx = await esbuild.context({
        ...clientBuildOptions,
        plugins: [
            ...clientBuildOptions.plugins,
            {
                name: 'html-inject-plugin',
                setup(build) {
                    build.onEnd(async () => {
                        await buildCSS();
                        injectAssets();
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
    injectAssets();
    console.error('[revelt] built → dist/render-server.cjs and dist/client/client.js');
}
