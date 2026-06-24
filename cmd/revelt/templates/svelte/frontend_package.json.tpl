{
  "name": "revelt-svelte-frontend",
  "version": "1.0.0",
  "private": true,
  "type": "module",
  "scripts": {
    "build": "node build.mjs",
    "build:watch": "node build.mjs --watch"
  },
  "dependencies": {
    "svelte": "^5.56.3"
  },
  "devDependencies": {
   "@sveltejs/vite-plugin-svelte": "^5.0.3",
    "typescript": "^5.5.4",
    "vite": "^6.3.0"{{TAILWIND_DEPS}}
  }
}
