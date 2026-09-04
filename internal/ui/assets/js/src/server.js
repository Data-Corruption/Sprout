// --- FILE service.https ---

// Server Actions
// Stop, restart, update, and shared restart polling functionality

import { blockClicks, unblockClicks, showDialog, showError } from './ui.js';
import { getJSON, postJSON } from './api.js';

/** Stop the server. */
export async function stopServer() {
    blockClicks();
    try {
        await postJSON('/settings/stop', {});
        document.title = 'Server Stopped';
        document.body.className = 'bg-base-100 min-h-screen flex items-center justify-center';
        document.body.innerHTML = `
            <div class="text-center">
                <h1 class="text-2xl font-bold mb-2">Server Stopped</h1>
                <p class="text-base-content/70">You can close this tab.</p>
            </div>
        `;
    } catch (err) {
        unblockClicks();
        showError(`Stop failed: ${err.message}`);
    }
}

/** Restart the server without changing the installed version. */
export async function restartServer() {
    blockClicks();
    try {
        await postJSON('/settings/restart', {});
        setTimeout(() => pollForRestart('restart'), 3000);
    } catch (err) {
        unblockClicks();
        showError(`Restart could not start: ${err.message}`);
    }
}

// --- BEGIN update.self ---
/** Check freshness, then launch an update when one is available. */
export async function updateServer() {
    blockClicks();
    try {
        const result = await postJSON('/settings/update', {});
        if (result?.status === 'current') {
            unblockClicks();
            showDialog({
                title: 'Already Current',
                message: result.message ?? 'Already running the latest version.',
                tone: 'success',
                confirmLabel: 'Close',
            });
            return;
        }
        setTimeout(() => pollForRestart('update'), 3000);
    } catch (err) {
        unblockClicks();
        showError(`Update could not start: ${err.message}`);
    }
}

// --- END update.self ---

/** Poll for restart or update completion. */
export function pollForRestart(action = 'restart') {
    const startTime = Date.now();
    const pollInterval = 3000;
    const timeout = 300000;

    const check = async () => {
        if (Date.now() - startTime > timeout) {
            unblockClicks();
            const label = action === 'update' ? 'Update' : 'Restart';
            showError(`${label} timed out. Check the service and update logs.`);
            return;
        }

        try {
            const data = await getJSON(`/settings/restart-status?t=${Date.now()}`);
            if (!data?.restarted) {
                setTimeout(check, pollInterval);
                return;
            }

            // --- BEGIN update.self ---
            if (action === 'update' && !data.updated) {
                unblockClicks();
                showError('The service restarted, but the update did not apply. Check the update logs.');
                return;
            }
            // --- END update.self ---

            window.location.reload();
        } catch {
            // A network failure is expected while the service is down.
            setTimeout(check, pollInterval);
        }
    };

    check();
}

/** Wire up server control buttons through the shared dialog. */
export function initServerControls() {
    document.getElementById('settings-stop-btn')?.addEventListener('click', () => {
        showDialog({
            title: 'Stop Server',
            message: 'Stop the service? You will lose access to this page.',
            tone: 'error',
            confirmLabel: 'Stop',
            onConfirm: stopServer,
            onCancel: () => {},
        });
    });
    document.getElementById('settings-restart-btn')?.addEventListener('click', () => {
        showDialog({
            title: 'Restart Server',
            message: 'Restart the service now?',
            tone: 'warning',
            confirmLabel: 'Restart',
            onConfirm: restartServer,
            onCancel: () => {},
        });
    });
    // --- BEGIN update.self ---
    document.getElementById('settings-update-btn')?.addEventListener('click', () => {
        showDialog({
            title: 'Update Server',
            message: 'Check for and apply an available update?',
            tone: 'warning',
            confirmLabel: 'Update',
            onConfirm: updateServer,
            onCancel: () => {},
        });
    });
    // --- END update.self ---
}
