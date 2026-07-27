// Behaviour for the authenticated admin UI.
//
// Everything here is bound by delegation from data attributes rather than
// inline handlers, which lets the Content-Security-Policy forbid inline script
// entirely: an injected string in an event title cannot become executable code.
(function () {
    'use strict';

    function csrfToken() {
        var meta = document.querySelector('meta[name="csrf-token"]');
        return meta ? meta.getAttribute('content') : '';
    }

    // Attach the CSRF token to every htmx request.
    document.addEventListener('htmx:configRequest', function (event) {
        event.detail.headers['X-CSRF-Token'] = csrfToken();
    });

    // --- Mobile navigation -------------------------------------------------

    function toggleMobileMenu(force) {
        var nav = document.getElementById('mobileNav');
        if (!nav) {
            return;
        }
        var open = typeof force === 'boolean' ? force : !nav.classList.contains('open');
        nav.classList.toggle('open', open);

        var menuIcon = document.querySelector('.menu-icon');
        var closeIcon = document.querySelector('.close-icon');
        if (menuIcon) {
            menuIcon.style.display = open ? 'none' : 'block';
        }
        if (closeIcon) {
            closeIcon.style.display = open ? 'block' : 'none';
        }
    }

    // --- Revoke confirmation ----------------------------------------------

    function openRevokeModal(button) {
        var modal = document.getElementById('revoke-modal');
        var form = document.getElementById('revoke-form');
        if (!modal || !form) {
            return;
        }

        document.getElementById('revoke-key-name').textContent = button.getAttribute('data-key-name');
        form.action = button.getAttribute('data-revoke-url');
        document.getElementById('revoke-csrf').value = csrfToken();
        modal.classList.add('active');
    }

    function closeRevokeModal() {
        var modal = document.getElementById('revoke-modal');
        if (modal) {
            modal.classList.remove('active');
        }
    }

    // --- Notification provider tests --------------------------------------

    function showToast(message, ok) {
        var existing = document.querySelector('.toast');
        if (existing) {
            existing.remove();
        }

        var toast = document.createElement('div');
        toast.className = 'toast ' + (ok ? 'toast-success' : 'toast-error');

        var text = document.createElement('span');
        text.textContent = message;
        toast.appendChild(text);

        var close = document.createElement('button');
        close.type = 'button';
        close.className = 'toast-close';
        close.setAttribute('aria-label', 'Dismiss');
        close.textContent = '×';
        close.addEventListener('click', function () {
            toast.remove();
        });
        toast.appendChild(close);

        document.body.appendChild(toast);
        setTimeout(function () {
            toast.remove();
        }, 6000);
    }

    function testProvider(button) {
        var provider = button.getAttribute('data-test-provider');
        var original = button.textContent;

        button.disabled = true;
        button.textContent = 'Sending...';

        var body = new URLSearchParams();
        body.set('provider', provider);
        body.set('csrf_token', csrfToken());

        fetch('/settings/test-notification', {
            method: 'POST',
            headers: {
                'Content-Type': 'application/x-www-form-urlencoded',
                'X-CSRF-Token': csrfToken()
            },
            body: body.toString(),
            credentials: 'same-origin'
        }).then(function (response) {
            return response.json().then(function (data) {
                showToast(data.message || 'Test complete', response.ok && data.success);
            });
        }).catch(function () {
            showToast('Could not reach the server', false);
        }).finally(function () {
            button.disabled = false;
            button.textContent = original;
        });
    }

    // --- Wiring ------------------------------------------------------------

    document.addEventListener('click', function (event) {
        var target = event.target;

        if (target.closest('[data-action="toggle-menu"]')) {
            toggleMobileMenu();
            return;
        }

        var revokeButton = target.closest('[data-revoke-url]');
        if (revokeButton) {
            openRevokeModal(revokeButton);
            return;
        }

        if (target.closest('[data-action="close-modal"]') || target.classList.contains('modal-overlay')) {
            closeRevokeModal();
            return;
        }

        var testButton = target.closest('[data-test-provider]');
        if (testButton) {
            testProvider(testButton);
            return;
        }

        var clearPin = target.closest('[data-action="clear-pin"]');
        if (clearPin) {
            var field = document.getElementById('clear_pin');
            if (field && clearPin.form) {
                field.value = '1';
                clearPin.form.submit();
            }
            return;
        }

        // Clicking outside an open mobile menu closes it.
        var nav = document.getElementById('mobileNav');
        if (nav && nav.classList.contains('open') &&
            !nav.contains(target) && !target.closest('.mobile-menu-btn')) {
            toggleMobileMenu(false);
        }
    });

    document.addEventListener('keydown', function (event) {
        if (event.key === 'Escape') {
            closeRevokeModal();
        }
    });

    // Reveal a provider's fields when it is switched on.
    document.addEventListener('change', function (event) {
        var toggle = event.target.closest('[data-toggles]');
        if (!toggle) {
            return;
        }
        var fields = document.getElementById(toggle.getAttribute('data-toggles'));
        if (fields) {
            fields.style.display = toggle.checked ? 'block' : 'none';
        }
    });

    // Copy-to-clipboard for a newly created API key, which is shown once.
    document.addEventListener('click', function (event) {
        var button = event.target.closest('[data-copy-target]');
        if (!button) {
            return;
        }

        var source = document.getElementById(button.getAttribute('data-copy-target'));
        if (!source || !navigator.clipboard) {
            return;
        }

        navigator.clipboard.writeText(source.textContent).then(function () {
            var label = button.querySelector('[data-copy-label]') || button;
            var original = label.textContent;
            label.textContent = 'Copied';
            setTimeout(function () {
                label.textContent = original;
            }, 2000);
        });
    });
})();
