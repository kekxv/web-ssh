(function() {
    'use strict';

    function addReplaceChildren(target) {
        if (!target || target.replaceChildren) {
            return;
        }

        target.replaceChildren = function() {
            var i;
            var item;

            while (this.firstChild) {
                this.removeChild(this.firstChild);
            }

            for (i = 0; i < arguments.length; i += 1) {
                item = arguments[i];
                this.appendChild(item instanceof Node ? item : document.createTextNode(String(item)));
            }
        };
    }

    addReplaceChildren(typeof Element === 'undefined' ? null : Element.prototype);
    addReplaceChildren(typeof DocumentFragment === 'undefined' ? null : DocumentFragment.prototype);
}());
