// Form Handlers
// Generic handlers for selects and text inputs with debouncing

import { findStatus, showPending, showSuccess, showError } from './ui.js';
import { postJSON } from './api.js';

/**
 * Generic handler for select dropdowns (immediate POST on change)
 * @param {string|HTMLElement} inputOrId - Input element or ID
 * @param {string} endpoint - POST endpoint
 * @param {string} fieldName - JSON field name
 * @param {Function} [onSuccess] - Optional success callback
 */
export function handleSelect(inputOrId, endpoint, fieldName, onSuccess) {
    const input = typeof inputOrId === 'string'
        ? document.getElementById(inputOrId)
        : inputOrId;
    if (!input) return;

    const status = findStatus(input);

    input.addEventListener('change', async () => {
        showPending(status);
        try {
            await postJSON(endpoint, { [fieldName]: input.value });
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
 * @param {string} endpoint - POST endpoint
 * @param {string} fieldName - JSON field name
 * @param {number} [debounceMs=500] - Debounce delay in milliseconds
 * @param {object} [opts] - Options: { skipEmpty, onSuccess }
 */
export function handleTextInput(inputOrId, endpoint, fieldName, debounceMs = 500, opts = {}) {
    const input = typeof inputOrId === 'string'
        ? document.getElementById(inputOrId)
        : inputOrId;
    if (!input) return;

    const status = findStatus(input);

    let timeout = null;
    let controller = null;

    input.addEventListener('input', () => {
        clearTimeout(timeout);
        if (controller) controller.abort();

        timeout = setTimeout(async () => {
            // Skip empty values for optional fields like bot token
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

                await postJSON(endpoint, { [fieldName]: value }, controller.signal);
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
