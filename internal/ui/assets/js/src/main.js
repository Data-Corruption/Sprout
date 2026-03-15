// Main Entry Point
// Initializes all modules and sets up global functions for HTML onclick handlers

import { initTheme, setupThemeToggle, toggleTheme } from './theme.js';
import { blockClicks, unblockClicks } from './ui.js';
import { stopServer, restartServer } from './server.js';
import { initSettings } from './settings.js';

// Initialize theme immediately (before DOM ready) to prevent flash
initTheme();

// Expose functions needed by inline onclick handlers in HTML
window.toggleTheme = toggleTheme;
window.stopServer = stopServer;
window.restartServer = restartServer;
window.blockClicks = blockClicks;
window.unblockClicks = unblockClicks;

// Setup after DOM is loaded
document.addEventListener('DOMContentLoaded', () => {
    setupThemeToggle();
    initSettings();
});
