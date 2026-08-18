const assert = require('node:assert/strict');
const acorn = require('acorn');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

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

test('terminal runtime does not require Element.replaceChildren', () => {
    const terminalSource = fs.readFileSync(path.resolve(__dirname, '..', 'vendor', 'xterm.min.js'), 'utf8');
    assert.equal(terminalSource.includes('.replaceChildren('), false);
});

test('page loads the local browser compatibility layer before vendor scripts', () => {
    const page = fs.readFileSync(path.resolve(__dirname, '..', 'index.html'), 'utf8');
    const compatIndex = page.indexOf('/vendor/browser-compat.js');
    const xtermIndex = page.indexOf('/vendor/xterm.min.js');

    assert.notEqual(compatIndex, -1);
    assert.ok(compatIndex < xtermIndex);
});

test('browser compatibility layer supplies replaceChildren to legacy DOM elements', () => {
    function FakeNode(value) {
        this.value = value;
    }

    function FakeElement() {
        this.children = [];
    }

    Object.defineProperty(FakeElement.prototype, 'firstChild', {
        get: function() { return this.children[0] || null; }
    });
    FakeElement.prototype.appendChild = function(child) {
        this.children.push(child);
        return child;
    };
    FakeElement.prototype.removeChild = function(child) {
        const index = this.children.indexOf(child);
        this.children.splice(index, 1);
        return child;
    };

    const context = {
        Element: FakeElement,
        Node: FakeNode,
        document: {
            createTextNode: function(value) { return new FakeNode(value); }
        }
    };
    const source = fs.readFileSync(path.resolve(__dirname, '..', 'vendor', 'browser-compat.js'), 'utf8');
    vm.runInNewContext(source, context);

    const element = new FakeElement();
    element.appendChild(new FakeNode('old'));
    const node = new FakeNode('node');
    element.replaceChildren('text', node);

    assert.equal(element.children.length, 2);
    assert.equal(element.children[0].value, 'text');
    assert.equal(element.children[1], node);
});

test('legacy CSS makes full-screen overlays fill the Chrome 75 viewport', () => {
    const page = fs.readFileSync(path.resolve(__dirname, '..', 'index.html'), 'utf8');
    const legacyCss = fs.readFileSync(path.resolve(__dirname, '..', 'css', 'legacy.css'), 'utf8');

    assert.notEqual(page.indexOf('/css/legacy.css'), -1);
    assert.match(legacyCss, /\.inset-0\s*\{[^}]*top:\s*0[^}]*right:\s*0[^}]*bottom:\s*0[^}]*left:\s*0[^}]*\}/);
});

test('file types resolve to compact local SVG icon names', () => {
    let appOptions;
    const source = fs.readFileSync(path.resolve(__dirname, 'app.js'), 'utf8');
    vm.runInNewContext(source, {
        Vue: {
            createApp: function(options) {
                appOptions = options;
                return { mount: function() {} };
            }
        },
        console: { warn: function() {} }
    });

    assert.equal(appOptions.methods.getFileIcon('backup.zip'), 'archive');
    assert.equal(appOptions.methods.getFileIcon('data.json'), 'settings');
    assert.equal(appOptions.methods.getFileIcon('docker_images'), 'file');
});
