import * as React from "react";
import { hydrateRoot, createRoot } from "react-dom/client";
import { COMPONENT_REGISTRY } from "revelt:registry";

function hydrateIslands() {
  const islands = document.querySelectorAll("[data-ssr-island]");

  for (const el of islands) {
    const name = el.getAttribute("data-ssr-island");
    const propsAttr = el.getAttribute("data-ssr-props");

    if (!name) continue;

    const props = propsAttr ? JSON.parse(propsAttr) : {};
    const entry = COMPONENT_REGISTRY.get(name);

    if (entry) {
      const { Component, mode } = entry;
      if (mode === "hydrate") {
        hydrateRoot(el, React.createElement(Component, props));
      } else if (mode === "client") {
        const root = createRoot(el);
        root.render(React.createElement(Component, props));
      }
    } else {
      console.warn(
        '[revelt-client] Component "' + name + '" not found in registry.',
      );
    }
  }
}

if (document.readyState === "loading") {
  document.addEventListener("DOMContentLoaded", hydrateIslands);
} else {
  hydrateIslands();
}
