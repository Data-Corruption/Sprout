// Theme Management
// Handles dark/light theme switching with localStorage and system preference support

const LIGHT_THEME = 'nord';
const DARK_THEME = 'forest';
const THEME_KEY = 'SPROUT_THEME';

/** Get current theme, defaulting to system preference */
export function getTheme() {
    return localStorage.getItem(THEME_KEY) ||
        (window.matchMedia?.('(prefers-color-scheme: dark)').matches ? DARK_THEME : LIGHT_THEME);
}

/** Check if current theme is dark */
export function isDarkTheme() {
    return getTheme() === DARK_THEME;
}

/** Update the theme toggle checkbox state */
function updateThemeToggle() {
    const toggle = document.getElementById('theme-toggle');
    if (toggle) {
        toggle.checked = isDarkTheme();
    }
}

/** Set theme and update UI */
export function setTheme(theme) {
    localStorage.setItem(THEME_KEY, theme);
    document.documentElement.setAttribute('data-theme', theme);
    updateThemeToggle();
}

/** Toggle between light and dark themes */
export function toggleTheme() {
    setTheme(isDarkTheme() ? LIGHT_THEME : DARK_THEME);
}

/** Initialize theme on page load */
export function initTheme() {
    const loadTheme = getTheme();
    document.documentElement.setAttribute('data-theme', loadTheme);
    localStorage.setItem(THEME_KEY, loadTheme);
}

/** Setup toggle after DOM is loaded */
export function setupThemeToggle() {
    updateThemeToggle();
}
