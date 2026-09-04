// --- FILE service.https ---

// Settings Wiring
// DOMContentLoaded initialization for all settings controls

import { handleSelect, handleTextInput } from './forms.js';

/** Show restart required notice */
function showRestartNotice() {
    const notice = document.getElementById('restart-required-notice');
    if (notice) notice.classList.remove('hidden');
}

// portFromBind extracts the numeric port from a host:port bind string.
function portFromBind(bind) {
    const i = (bind || '').lastIndexOf(':');
    if (i < 0) return 0;
    return parseInt(bind.slice(i + 1), 10) || 0;
}

function privilegedPortWarning() {
    const v = portFromBind(document.getElementById('settings-ui-bind')?.value);
    const notice = document.getElementById('privileged-port-notice');
    if (notice) notice.classList.toggle('hidden', !(v > 0 && v < 1024));
}

/** Wire up settings */
function wireSettings() {
    handleSelect('settings-log-level', '/settings', 'logLevel', showRestartNotice);
    handleTextInput('settings-ui-bind', '/settings', 'uiBind', 800, { onSuccess() { showRestartNotice(); privilegedPortWarning(); } });
    handleTextInput('settings-proxy-bind', '/settings', 'proxyBind', 800, { onSuccess: showRestartNotice });
}

/** Initialize all settings on DOMContentLoaded */
export function initSettings() {
    wireSettings();
    privilegedPortWarning();
}
