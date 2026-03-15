// Package ui provides embedded frontend templates and assets.
package ui

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed assets
var assetsFS embed.FS

//go:embed assets/manifest.json
var manifestData []byte // ignore lint err here, file is generated at build time

// Patterns to exclude from public serving (relative to assets/).
// Matched using filepath.Match semantics.
var ignorePatterns = []string{
	"css/input.css",   // Tailwind source
	"css/daisyui.mjs", // DaisyUI build deps
	"css/daisyui-theme.mjs",
	"js/src",        // JS source directory (prefix match)
	"js/src/*",      // JS source files
	"manifest.json", // The manifest itself
}

// Asset represents an embedded static asset with cache busting.
type Asset struct {
	RelPath     string // original relative path, e.g. "css/output.css"
	URLPath     string // cache-busted URL path, e.g. "/assets/css/output.a1b2c3d4.css"
	Data        []byte
	ContentType string
}

// Handler returns an http.HandlerFunc that serves this asset with
// appropriate caching headers (1 year, immutable).
func (a *Asset) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", a.ContentType)
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Write(a.Data)
	}
}

// UI holds parsed templates and static assets.
// Create once at app startup via New().
type UI struct {
	templates *template.Template
	Assets    map[string]*Asset // keyed by relative path (e.g. "css/output.css")

	// Convenience shortcuts to common assets
	CSS *Asset
	JS  *Asset

	// URL path -> Asset for routing
	routeMap map[string]*Asset
}

// New parses all embedded templates and loads static assets from the manifest.
func New() (*UI, error) {
	// Load manifest
	var manifest map[string]string // relPath -> hash
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse asset manifest: %w", err)
	}

	// Build asset map
	assets := make(map[string]*Asset)
	routeMap := make(map[string]*Asset)

	for relPath, hash := range manifest {
		// Skip ignored patterns
		if isIgnored(relPath) {
			continue
		}

		// Read file data
		data, err := assetsFS.ReadFile("assets/" + relPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read asset %s: %w", relPath, err)
		}

		// Build cache-busted URL path
		ext := filepath.Ext(relPath)
		base := strings.TrimSuffix(relPath, ext)
		urlPath := fmt.Sprintf("/assets/%s.%s%s", base, hash, ext)

		asset := &Asset{
			RelPath:     relPath,
			URLPath:     urlPath,
			Data:        data,
			ContentType: detectContentType(relPath),
		}

		assets[relPath] = asset
		routeMap[urlPath] = asset
	}

	// assetPath helper for templates - must define before parsing
	assetPath := func(relPath string) string {
		if asset, ok := assets[relPath]; ok {
			return asset.URLPath
		}
		return "/assets/" + relPath
	}

	// Parse templates with helper functions
	t, err := template.New("").Funcs(template.FuncMap{
		"assetPath": assetPath,
	}).ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, fmt.Errorf("failed to parse templates: %w", err)
	}

	return &UI{
		templates: t,
		Assets:    assets,
		routeMap:  routeMap,
		CSS:       assets["css/output.css"],
		JS:        assets["js/output.js"],
	}, nil
}

// Execute renders a template by name to the writer.
func (ui *UI) Execute(w io.Writer, name string, data any) error {
	return ui.templates.ExecuteTemplate(w, name, data)
}

// ServeAsset returns an http.HandlerFunc that routes to the correct asset
// based on the URL path. Mount this at "/assets/*".
func (ui *UI) ServeAsset(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if asset, ok := ui.routeMap[path]; ok {
		asset.Handler()(w, r)
		return
	}
	http.NotFound(w, r)
}

// isIgnored checks if a path matches any ignore pattern.
func isIgnored(relPath string) bool {
	for _, pattern := range ignorePatterns {
		// Check prefix match for directory patterns (e.g. "js/src")
		if strings.HasSuffix(pattern, "/") || !strings.Contains(pattern, "*") {
			if strings.HasPrefix(relPath, strings.TrimSuffix(pattern, "/")) {
				// But allow exact match of directory name as a file
				if !strings.Contains(pattern, "*") && relPath == pattern {
					return true
				}
				if strings.HasPrefix(relPath, pattern) || strings.HasPrefix(relPath, strings.TrimSuffix(pattern, "/*")+"/") {
					return true
				}
			}
		}
		// Check glob match
		if matched, _ := filepath.Match(pattern, relPath); matched {
			return true
		}
		// Also check just the filename for patterns without path separators
		if !strings.Contains(pattern, "/") {
			if matched, _ := filepath.Match(pattern, filepath.Base(relPath)); matched {
				return true
			}
		}
	}
	return false
}

// detectContentType returns the MIME type based on file extension.
func detectContentType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "application/javascript; charset=utf-8"
	case ".html":
		return "text/html; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".svg":
		return "image/svg+xml"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".eot":
		return "application/vnd.ms-fontobject"
	default:
		return "application/octet-stream"
	}
}

// WalkAssets walks the embedded asset filesystem, calling fn for each file.
// Useful for debugging or building custom asset handling.
func (ui *UI) WalkAssets(fn func(path string, d fs.DirEntry) error) error {
	return fs.WalkDir(assetsFS, "assets", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		return fn(path, d)
	})
}
