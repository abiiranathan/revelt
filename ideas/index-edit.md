esbuild doesn't have built-in HTML processing like Vite does — but there's a first-party plugin for exactly this: **`@shopify/esbuild-plugin-html`** or more commonly the community **`esbuild-plugin-html`**. However, neither is well-maintained.

The cleanest approach for your setup is **`@esbuilder/html`** or just using esbuild's `metafile` output to do it yourself in the build script. Given you already control `build.mjs`, the self-contained approach is the most robust.

---

## Option A — Write it yourself in `build.mjs` (recommended)

esbuild's `metafile: true` gives you the exact output filenames including content hashes. You read `index.html`, inject the script/link tags, write the result to `dist/client/index.html`.

```js
/** @type {esbuild.BuildOptions} */
const clientBuildOptions = {
    entryPoints: ['client.tsx'],
    bundle: true,
    platform: 'browser',
    format: 'iife',
    outdir: 'dist/client',          // <-- switch from outfile to outdir
    entryNames: '[name]-[hash]',    // enables content hashing
    target: 'es2020',
    jsx: 'automatic',
    resolveExtensions: ['.tsx', '.ts', '.jsx', '.js', '.json'],
    sourcemap: watchMode ? 'inline' : false,
    alias: { '@': resolve(__dirname, 'src') },
    plugins: [componentRegistryPlugin(clientComponents)],
    metafile: true,                 // <-- get output manifest
    minify: !watchMode,
};
```

Then after the build, inject into your HTML template:

```js
/**
 * Reads the client build metafile and injects <script> and <link> tags
 * into the HTML template, writing the result to dist/client/index.html.
 *
 * @param {import('esbuild').Metafile} metafile
 * @param {string} staticPrefix URL prefix configured in revelt.json
 */
function injectAssets(metafile, staticPrefix) {
    const outputs = metafile.outputs;

    const scripts = [];
    const styles = [];

    for (const [outPath, meta] of Object.entries(outputs)) {
        // outPath is relative to cwd, e.g. "frontend/dist/client/client-ABC123.js"
        // Strip the "dist/client/" prefix to get the URL path segment.
        const filename = basename(outPath);

        if (meta.entryPoint && filename.endsWith('.js')) {
            scripts.push(`<script src="${staticPrefix}${filename}" defer></script>`);
        }
        if (filename.endsWith('.css')) {
            styles.push(`<link rel="stylesheet" href="${staticPrefix}${filename}">`);
        }
    }

    const templatePath = resolve(__dirname, 'index.html');
    let html = fs.readFileSync(templatePath, 'utf8');

    // Replace placeholder comments or inject before </head> and </body>.
    html = html.replace('</head>', styles.join('\n    ') + '\n</head>');
    html = html.replace('</body>', scripts.join('\n    ') + '\n</body>');

    const outPath = resolve(__dirname, 'dist/client/index.html');
    fs.mkdirSync(dirname(outPath), { recursive: true });
    fs.writeFileSync(outPath, html, 'utf8');

    console.error('[revelt] wrote dist/client/index.html');
}
```

And call it after the build:

```js
if (watchMode) {
    const serverCtx = await esbuild.context(serverBuildOptions);
    const clientCtx = await esbuild.context(clientBuildOptions);
    await serverCtx.watch();

    // esbuild's watch API fires onEnd after every rebuild.
    const clientCtxWithHtml = await esbuild.context({
        ...clientBuildOptions,
        plugins: [
            ...clientBuildOptions.plugins,
            {
                name: 'html-inject',
                setup(build) {
                    build.onEnd((result) => {
                        if (result.metafile) {
                            injectAssets(result.metafile, config.static_prefix ?? '/static/');
                        }
                    });
                },
            },
        ],
    });

    await clientCtxWithHtml.watch();
    console.error('[revelt] watching frontend files for changes...');
} else {
    const serverResult = await esbuild.build(serverBuildOptions);
    const clientResult = await esbuild.build(clientBuildOptions);

    if (serverResult.errors.length > 0 || clientResult.errors.length > 0) {
        process.exit(1);
    }

    injectAssets(clientResult.metafile, config.static_prefix ?? '/static/');
    console.error('[revelt] built → dist/render-server.cjs and dist/client/');
}
```

---

## Option B — `esbuild-plugin-html` package

If you'd rather not manage injection yourself:

```bash
npm install --save-dev @craftamap/esbuild-plugin-html
```

```js
import { htmlPlugin } from '@craftamap/esbuild-plugin-html';

const clientBuildOptions = {
    entryPoints: ['client.tsx'],
    bundle: true,
    outdir: 'dist/client',         // required by this plugin
    metafile: true,                // required by this plugin
    // ...
    plugins: [
        componentRegistryPlugin(clientComponents),
        htmlPlugin({
            files: [
                {
                    entryPoints: ['client.tsx'],
                    filename: 'index.html',
                    htmlTemplate: fs.readFileSync('index.html', 'utf8'),
                    scriptLoading: 'defer',
                },
            ],
        }),
    ],
};
```

This handles injection automatically but adds a dependency and you lose fine-grained control over where tags are placed. Given your setup already reads `revelt.json` and has `staticPrefix` logic, Option A fits better — you stay in full control of the output and it's only ~20 lines.

---

## One consequence for the Go side

If you adopt content hashing (`[name]-[hash]`), the Go server can no longer hardcode `client.js` in the static file server. The generated `dist/client/index.html` becomes the source of truth for asset URLs. Your Go handler would serve `dist/client/index.html` directly as a static file instead of rendering it through `html/template` — or you read the metafile from disk at startup and extract the hashed filenames.

The simplest path: **skip content hashing for now**, keep `outfile: 'dist/client/client.js'`, and just use the `injectAssets` approach without `[name]-[hash]`. You get automatic HTML updating without the hashing complexity. Add hashing later when you need cache busting.
