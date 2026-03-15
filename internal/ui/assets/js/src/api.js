// API Helpers
// Unified fetch wrappers with error handling

/**
 * POST JSON to an endpoint
 * @param {string} endpoint - URL to POST to
 * @param {object} body - JSON body
 * @param {AbortSignal} [signal] - Optional abort signal
 * @returns {Promise<Response>}
 * @throws {Error} with error message from response
 */
export async function postJSON(endpoint, body, signal) {
    const res = await fetch(endpoint, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body),
        signal
    });
    if (!res.ok) {
        const text = await res.text();
        throw new Error(text || `HTTP ${res.status}`);
    }
    return res;
}

/**
 * GET JSON from an endpoint
 * @param {string} endpoint - URL to GET
 * @returns {Promise<any>}
 * @throws {Error} with error message from response
 */
export async function getJSON(endpoint) {
    const res = await fetch(endpoint);
    if (!res.ok) {
        const text = await res.text();
        throw new Error(text || `HTTP ${res.status}`);
    }
    return res.json();
}
