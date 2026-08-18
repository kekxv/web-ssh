const assert = require('node:assert/strict');
const acorn = require('acorn');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const management = require('./management.js');

test('remote management endpoints include the proxy session id', () => {
    const endpoint = management.managementEndpoint({
        isLocalMode: false,
        isRemoteLocalMode: true,
        sessionId: 'remote-id'
    }, 'system/info');

    assert.equal(endpoint, '/api/remote/system/info?session_id=remote-id');
});

test('remote management endpoints preserve allowed request parameters', () => {
    const endpoint = management.managementEndpoint({
        isLocalMode: false,
        isRemoteLocalMode: true,
        sessionId: 'remote-id'
    }, 'docker/container/logs', { id: 'container-1', tail: 500 });

    assert.equal(endpoint, '/api/remote/docker/container/logs?id=container-1&tail=500&session_id=remote-id');
});

test('direct SSH does not support management panels', () => {
    assert.equal(management.managementSupported({
        isLocalMode: false,
        isRemoteLocalMode: false
    }), false);
});

test('local sessions support management panels', () => {
    assert.equal(management.managementSupported({
        isLocalMode: true,
        isRemoteLocalMode: false
    }), true);
});

test('browser assets do not use unsupported Chrome 75 syntax', () => {
    const root = path.resolve(__dirname, '..', '..');
    const page = fs.readFileSync(path.join(root, 'static/index.html'), 'utf8');
    const scripts = Array.from(page.matchAll(/<script src="([^"]+)"/g), function(match) { return match[1]; });

    for (const script of scripts) {
        const assetPath = script.split('?')[0];
        const source = fs.readFileSync(path.join(root, 'static', assetPath.replace(/^\//, '')), 'utf8');
        assert.doesNotThrow(function() {
            acorn.parse(source, { ecmaVersion: 2019, sourceType: 'script' });
        }, script + ' is not parseable by Chrome 75');
    }
});

test('page does not load the Tailwind browser runtime or emoji glyphs', () => {
    const page = fs.readFileSync(path.resolve(__dirname, '..', 'index.html'), 'utf8');
    assert.equal(page.includes('/vendor/tailwind.js'), false);
    assert.equal(/[\u{1F000}-\u{1FAFF}]/u.test(page), false);
});
