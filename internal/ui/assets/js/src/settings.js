// Settings Wiring
// DOMContentLoaded initialization for all settings controls

import { handleSelect, handleTextInput } from './forms.js';

/** Show restart required notice */
function showRestartNotice() {
    const notice = document.getElementById('restart-required-notice');
    if (notice) notice.classList.remove('hidden');
}

/** Wire up settings */
function wireSettings() {
    handleSelect('settings-log-level', '/settings', 'logLevel', showRestartNotice);
    handleTextInput('settings-host', '/settings', 'host', 500, { onSuccess: showRestartNotice });
    handleTextInput('settings-port', '/settings', 'port', 500, { onSuccess: showRestartNotice });
    handleTextInput('settings-proxy-port', '/settings', 'proxyPort', 500, { onSuccess: showRestartNotice });
}

/** Initialize all settings on DOMContentLoaded */
export function initSettings() {
    wireSettings();
}
