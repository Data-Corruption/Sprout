// --- FILE service.https ---

// UI Utilities
// Click blocker, status indicators, and common UI helpers

/** Show click blocker overlay */
export function blockClicks() {
    const blocker = document.getElementById('click-blocker');
    if (!blocker || blocker.open) return;
    blocker.showModal();
}

/** Hide click blocker overlay */
export function unblockClicks() {
    const blocker = document.getElementById('click-blocker');
    if (!blocker?.open) return;
    blocker.close();
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
    return input.parentElement?.querySelector('.status') || null;
}

/**
 * Show error modal with message.
 * Dual signature: showError(message) or showError(statusEl, message) - the
 * latter also clears the pending spinner on the status element.
 */
export function showError(statusOrMsg, maybeMsg) {
    const message = maybeMsg !== undefined ? maybeMsg : statusOrMsg;
    const statusEl = maybeMsg !== undefined ? statusOrMsg : null;

    if (statusEl && statusEl.classList) {
        statusEl.className = 'status hidden';
    }

    showDialog({
        title: 'Error',
        message,
        tone: 'error',
        confirmLabel: 'Close',
    });
}

/**
 * Show the shared alert/confirmation dialog.
 * Dynamic values are always assigned through textContent.
 */
export function showDialog({
    title,
    message,
    tone = 'info',
    confirmLabel = 'Close',
    cancelLabel = 'Cancel',
    onConfirm = null,
    onCancel = null,
} = {}) {
    const modal = document.getElementById('action-dialog');
    const titleEl = document.getElementById('action-dialog-title');
    const msgEl = document.getElementById('action-dialog-message');
    const confirmBtn = document.getElementById('action-dialog-confirm');
    const cancelForm = document.getElementById('action-dialog-cancel-form');
    const cancelBtn = document.getElementById('action-dialog-cancel');
    if (!modal || !titleEl || !msgEl || !confirmBtn || !cancelForm || !cancelBtn) return;

    const tones = {
        info: { title: 'text-info', button: 'btn-primary' },
        success: { title: 'text-success', button: 'btn-success' },
        warning: { title: 'text-warning', button: 'btn-warning' },
        error: { title: 'text-error', button: 'btn-error' },
    };
    const style = tones[tone] ?? tones.info;

    titleEl.textContent = title ?? 'Notice';
    titleEl.className = `font-bold text-lg ${style.title}`;
    msgEl.textContent = message ?? '';
    confirmBtn.textContent = confirmLabel;
    confirmBtn.className = `btn ${style.button}`;
    confirmBtn.onclick = async () => {
        modal.close();
        if (onConfirm) await onConfirm();
    };

    cancelForm.classList.toggle('hidden', !onCancel);
    cancelBtn.textContent = cancelLabel;
    cancelBtn.onclick = () => {
        if (onCancel) onCancel();
    };

    modal.showModal();
}
