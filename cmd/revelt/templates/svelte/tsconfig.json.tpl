{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["DOM", "DOM.Iterable", "ES2022"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "strict": true,
    "esModuleInterop": true,
    "skipLibCheck": true,
    "noEmit": true,
    "allowJs": true
  },
  "include": ["build.mjs", "render-server.js", "**/*.ts", "**/*.svelte"],
  "exclude": [
    "node_modules",
    "dist"
  ]
}