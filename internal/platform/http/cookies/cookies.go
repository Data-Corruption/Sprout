// --- FILE service.https ---

package cookies

import (
	"net/http"
	"time"
)

// Read returns the value of the named cookie from the request.
// Returns an empty string if the cookie is not found.
func Read(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// Set sets an HTTP-only cookie on the response.
// The cookie is configured with SameSite=Strict.
func Set(w http.ResponseWriter, name, value, path string, maxAge time.Duration, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   int(maxAge.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}
