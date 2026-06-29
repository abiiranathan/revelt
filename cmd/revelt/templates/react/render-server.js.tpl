import * as React from 'react';
import { renderToString } from 'react-dom/server';
import { createInterface } from 'node:readline';
import { COMPONENT_REGISTRY } from 'revelt:registry';

process.stdin.setEncoding('utf8');

const rl = createInterface({
    input: process.stdin,
    crlfDelay: Infinity,
});

rl.on('line', (line) => {
    const trimmed = line.trim();
    if (trimmed.length === 0) return;

    let req;
    try {
        req = JSON.parse(trimmed);
    } catch (err) {
        respond({ id: 0, error: 'JSON parse error: ' + err.message });
        return;
    }

    handleRequest(req);
});

rl.on('close', () => {
    process.exit(0);
});

function escapeHtml(text) {
    return text
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#039;');
}

function handleRequest(req) {
    const { id, component, props = {} } = req;

    if (typeof component !== 'string' || component.length === 0) {
        respond({ id, error: 'missing or empty "component" field' });
        return;
    }

    const entry = COMPONENT_REGISTRY.get(component);
    if (entry == null) {
        respond({ id, error: 'unknown component: "' + component + '"' });
        return;
    }

    const { Component } = entry;

    try {
        const html = renderToString(React.createElement(Component, props));
        const serializedProps = escapeHtml(JSON.stringify(props));
        const wrappedHtml = '<div data-ssr-island="' + component + '" data-ssr-props="' + serializedProps + '">' + html + '</div>';

        respond({ id, html: wrappedHtml, head: '' });
    } catch (err) {
        process.stderr.write('[revelt] render error for "' + component + '": ' + (err.stack ?? err.message) + '\n');
        respond({ id, error: err.message });
    }
}

function respond(obj) {
    process.stdout.write(JSON.stringify(obj) + '\n');
}
