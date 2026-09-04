// --- FILE service.https ---

// Form Handlers
// Generic handlers for selects, text inputs (debounced), and toggles

import { findStatus, showPending, showSuccess, showError } from './ui.js';
import { requestJSON } from './api.js';

function getInput(inputOrId) {
    return typeof inputOrId === 'string'
        ? document.getElementById(inputOrId)
        : inputOrId;
}

function submitField(endpoint, fieldName, value, method, signal) {
    return requestJSON(endpoint, { method, body: { [fieldName]: value }, signal });
}

/**
 * Generic handler for select dropdowns (immediate submit on change)
 * @param {string|HTMLElement} inputOrId - Input element or ID
 * @param {string} endpoint - Endpoint to submit to
 * @param {string} fieldName - JSON field name
 * @param {Function} [onSuccess] - Optional success callback
 * @param {object} [opts] - Options: { method }
 */
export function handleSelect(inputOrId, endpoint, fieldName, onSuccess, opts = {}) {
    const input = getInput(inputOrId);
    if (!input) return;

    const status = findStatus(input);
    const method = opts.method || 'POST';

    input.addEventListener('change', async () => {
        showPending(status);
        try {
            await submitField(endpoint, fieldName, input.value, method);
            showSuccess(status);
            if (onSuccess) onSuccess();
        } catch (e) {
            showError(status, e.message);
        }
    });
}

/**
 * Generic handler for text/number inputs with debouncing
 * @param {string|HTMLElement} inputOrId - Input element or ID
 * @param {string} endpoint - Endpoint to submit to
 * @param {string} fieldName - JSON field name
 * @param {number} [debounceMs=500] - Debounce delay in milliseconds
 * @param {object} [opts] - Options: { skipEmpty, onSuccess, validate, method }
 */
export function handleTextInput(inputOrId, endpoint, fieldName, debounceMs = 500, opts = {}) {
    const input = getInput(inputOrId);
    if (!input) return;

    const status = findStatus(input);
    const method = opts.method || 'POST';

    let timeout = null;
    let controller = null;

    input.addEventListener('input', () => {
        clearTimeout(timeout);
        if (controller) controller.abort();

        timeout = setTimeout(async () => {
            // Skip empty values for optional fields
            if (opts.skipEmpty && !input.value.trim()) return;

            controller = new AbortController();
            showPending(status);
            try {
                let value = input.value;
                // Parse as int for number inputs
                if (input.type === 'number') {
                    value = parseInt(value, 10);
                    if (isNaN(value)) {
                        throw new Error('Invalid number');
                    }
                }

                if (opts.validate) {
                    const err = opts.validate(value);
                    if (err) { showError(status, err); return; }
                }

                await submitField(endpoint, fieldName, value, method, controller.signal);
                showSuccess(status);
                if (opts.onSuccess) opts.onSuccess();
            } catch (e) {
                if (e.name !== 'AbortError') {
                    showError(status, e.message);
                }
            }
        }, debounceMs);
    });
}

/**
 * Generic handler for checkbox toggles (immediate submit on change)
 * @param {string|HTMLElement} inputOrId - Input element or ID
 * @param {string} endpoint - Endpoint to submit to
 * @param {string} fieldName - JSON field name
 * @param {Function} [onSuccess] - Optional success callback
 * @param {object} [opts] - Options: { method }
 */
export function handleToggle(inputOrId, endpoint, fieldName, onSuccess, opts = {}) {
    const input = getInput(inputOrId);
    if (!input) return;

    const status = findStatus(input);
    const method = opts.method || 'POST';

    input.addEventListener('change', async () => {
        showPending(status);
        try {
            await submitField(endpoint, fieldName, input.checked, method);
            showSuccess(status);
            if (onSuccess) onSuccess();
        } catch (e) {
            showError(status, e.message);
        }
    });
}
