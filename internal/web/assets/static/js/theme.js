// Theme selection, shared by every page including the public approval page.
//
// The choice is stored locally and applied to the document root, which the
// stylesheet keys its light and dark variables off.
(function () {
    'use strict';

    var STORAGE_KEY = 'schedlock-theme';
    var root = document.documentElement;

    function currentTheme() {
        try {
            return localStorage.getItem(STORAGE_KEY) || 'system';
        } catch (e) {
            // Storage can be unavailable in private modes; the default still works.
            return 'system';
        }
    }

    function applyTheme(theme) {
        root.setAttribute('data-theme', theme);
        document.querySelectorAll('[data-theme-value]').forEach(function (button) {
            button.classList.toggle('active', button.getAttribute('data-theme-value') === theme);
        });
    }

    function selectTheme(theme) {
        try {
            localStorage.setItem(STORAGE_KEY, theme);
        } catch (e) {
            // Ignore: the theme still applies for this page view.
        }
        applyTheme(theme);
    }

    applyTheme(currentTheme());

    document.addEventListener('click', function (event) {
        var button = event.target.closest('[data-theme-value]');
        if (button) {
            selectTheme(button.getAttribute('data-theme-value'));
        }
    });
})();
