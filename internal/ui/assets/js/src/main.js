// --- FILE service.https ---

// Main Entry Point
// Initializes all modules; controls are wired with event listeners (no window.* globals)

import { initTheme, setupThemeToggle } from './theme.js';
import { initServerControls } from './server.js';
import { initSettings } from './settings.js';

// Initialize theme immediately (before DOM ready) to prevent flash
initTheme();

// Setup after DOM is loaded
document.addEventListener('DOMContentLoaded', () => {
    setupThemeToggle();
    initServerControls();
    initSettings();
});
