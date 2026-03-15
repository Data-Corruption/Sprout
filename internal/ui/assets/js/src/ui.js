// UI Utilities
// Click blocker, status indicators, and common UI helpers

/** Show click blocker overlay */
export function blockClicks() {
    const blocker = document.getElementById('click-blocker');
    if (blocker) blocker.classList.remove('hidden');
}

/** Hide click blocker overlay */
export function unblockClicks() {
    const blocker = document.getElementById('click-blocker');
    if (blocker) blocker.classList.add('hidden');
}

/** Show a loading spinner on the status element */
export function showPending(statusEl) {
    if (!statusEl) return;
    statusEl.className = 'status loading loading-spinner loading-xs';
    statusEl.textContent = '';
    statusEl.dataset.errorMessage = '';
    statusEl.onclick = null;
}

/** Show a green circle that auto-hides after 2 seconds */
export function showSuccess(statusEl) {
    if (!statusEl) return;
    statusEl.className = 'status status-success';
    statusEl.dataset.errorMessage = '';
    statusEl.onclick = null;
    setTimeout(() => {
        if (statusEl.classList.contains('status-success')) {
            statusEl.className = 'status hidden';
        }
    }, 2000);
}

/** Find the status element relative to the input */
export function findStatus(input) {
    // For inline toggles (inside label), find sibling status span
    const label = input.closest('label');
    if (label) {
        const status = label.querySelector('.status');
        if (status) return status;
    }
    // For inputs with wrapper divs, find sibling
    const wrapper = input.closest('.flex');
    if (wrapper) {
        const status = wrapper.querySelector('.status');
        if (status) return status;
    }
    // Fallback: search in parent form-control
    const formControl = input.closest('.form-control');
    if (formControl) {
        return formControl.querySelector('.status');
    }
    return null;
}

/** Show error modal with message */
export function showError(message) {
    const modal = document.getElementById('error-modal');
    const msgEl = document.getElementById('error-modal-message');
    if (modal && msgEl) {
        msgEl.textContent = message;
        modal.showModal();
    }
}
