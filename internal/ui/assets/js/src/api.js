// --- FILE service.https ---

// API Helpers
// Unified fetch wrappers with structured error handling

async function parseResponse(res) {
    const text = await res.text();
    if (!text) return null;
    try {
        return JSON.parse(text);
    } catch {
        return text;
    }
}

async function getErrorMessage(res) {
    const parsed = await parseResponse(res);
    if (parsed && typeof parsed === 'object') {
        if (typeof parsed.error === 'string') return parsed.error;
        if (typeof parsed.message === 'string') return parsed.message;
    }
    if (typeof parsed === 'string' && parsed) return parsed;
    return `HTTP ${res.status}`;
}

export async function requestJSON(endpoint, { method = 'GET', body, signal } = {}) {
    const res = await fetch(endpoint, {
        method,
        headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal,
    });
    if (!res.ok) {
        throw new Error(await getErrorMessage(res));
    }
    return parseResponse(res);
}

export function getJSON(endpoint, signal) {
    return requestJSON(endpoint, { method: 'GET', signal });
}

export function postJSON(endpoint, body, signal) {
    return requestJSON(endpoint, { method: 'POST', body, signal });
}

export function patchJSON(endpoint, body, signal) {
    return requestJSON(endpoint, { method: 'PATCH', body, signal });
}

export function putJSON(endpoint, body, signal) {
    return requestJSON(endpoint, { method: 'PUT', body, signal });
}

export function deleteJSON(endpoint, body, signal) {
    return requestJSON(endpoint, { method: 'DELETE', body, signal });
}
