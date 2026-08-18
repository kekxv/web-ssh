(function(root, factory) {
    var api = factory();
    if (typeof module === 'object' && module.exports) {
        module.exports = api;
    }
    root.WebSSHManagement = api;
}(typeof globalThis !== 'undefined' ? globalThis : this, function() {
    function managementSupported(state) {
        return state.isLocalMode === true || state.isRemoteLocalMode === true;
    }

    function managementEndpoint(state, path, params) {
        var base = state.isRemoteLocalMode ? '/api/remote/' : '/api/';
        var values = [];
        var key;
        params = params || {};

        for (key in params) {
            if (Object.prototype.hasOwnProperty.call(params, key) && params[key] !== '' && params[key] !== null && typeof params[key] !== 'undefined') {
                values.push(encodeURIComponent(key) + '=' + encodeURIComponent(params[key]));
            }
        }
        if (state.isRemoteLocalMode) {
            values.push('session_id=' + encodeURIComponent(state.sessionId));
        }

        return base + path + (values.length > 0 ? '?' + values.join('&') : '');
    }

    return {
        managementSupported: managementSupported,
        managementEndpoint: managementEndpoint
    };
}));
