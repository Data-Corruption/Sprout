// Server Actions
// Backup modal, stop, restart, and polling functionality

import { blockClicks, unblockClicks, showError } from './ui.js';

/** Stop the server */
export function stopServer() {
    blockClicks();
    fetch('/settings/stop', { method: 'POST' })
        .then(response => {
            if (response.ok) {
                // Replace title and body, keeping stylesheets loaded
                document.title = 'Server Stopped';
                document.body.className = 'bg-base-100 min-h-screen flex items-center justify-center';
                document.body.innerHTML = `
                    <div class="text-center">
                        <h1 class="text-2xl font-bold mb-2">Server Stopped</h1>
                        <p class="text-base-content/70">You can close this tab.</p>
                    </div>
                `;
            } else {
                throw new Error('Failed to stop server');
            }
        })
        .catch(err => {
            unblockClicks();
            showError('Error: ' + err.message);
        });
}

/** Restart the server with options from the restart modal */
export function restartServer() {
    const updateRequested = document.getElementById('restart-update').checked;

    // Close the modal
    document.getElementById('restart-modal').close();

    blockClicks();
    fetch('/settings/restart', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ update: updateRequested })
    })
        .then(response => {
            if (response.ok || response.status === 202) {
                // Server is restarting, poll for it to come back
                setTimeout(() => pollForRestart(updateRequested), 3000);
            } else {
                throw new Error('Failed to restart server');
            }
        })
        .catch(err => {
            unblockClicks();
            showError('Error: ' + err.message);
        });
}

/** Poll for server restart completion */
export function pollForRestart(updateRequested = false) {
    const startTime = Date.now();
    const pollInterval = 3000;
    const timeout = 300000; // 5 minutes

    const check = () => {
        if (Date.now() - startTime > timeout) {
            unblockClicks();
            showError('Restart timed out. Please check logs or try again.');
            return;
        }

        console.log('Polling for restart...', { updateRequested, time: Date.now() - startTime });
        fetch('/settings/restart-status?t=' + Date.now())
            .then(res => res.json())
            .then(data => {
                console.log('Poll response:', data);
                if (data.restarted) {
                    if (updateRequested && !data.updated) {
                        console.warn('Restart detected but not updated.', data);
                        unblockClicks();
                        showError('Restart completed, but the update did not apply. You may already be on the latest version, or the update failed.');
                    } else {
                        console.log('Restart success (updated=' + data.updated + '), reloading...');
                        window.location.reload();
                    }
                } else {
                    setTimeout(check, pollInterval);
                }
            })
            .catch(err => {
                console.error('Poll network error (expected if restarting):', err);
                // Network error during polling - server might be restarting
                setTimeout(check, pollInterval);
            });
    };

    check();
}
