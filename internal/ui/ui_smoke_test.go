// --- FILE service.https ---

package ui

import (
	"bytes"
	"html/template"
	"strings"
	"testing"
)

func TestTemplatesRender(t *testing.T) {
	u, err := New()
	if err != nil {
		t.Fatalf("ui.New: %v", err)
	}
	base := map[string]any{
		"CSS":     "/assets/css/output.css",
		"JS":      "/assets/js/output.js",
		"Favicon": template.URL("data:,"),
		"Version": "v0.0.0-dev",
	}
	pages := map[string]map[string]any{
		"login.html": {
			"Title":         "Login",
			"Error":         "invalid password",
			"NoCredentials": true,
			"AppName":       "sprout",
		},
		"settings.html": {
			"Title":     "Settings",
			"LogLevel":  "info",
			"UIBind":    ":8484",
			"ProxyBind": "",
		},
	}
	for name, extra := range pages {
		data := map[string]any{}
		for k, v := range base {
			data[k] = v
		}
		for k, v := range extra {
			data[k] = v
		}
		var buf bytes.Buffer
		if err := u.Execute(&buf, name, data); err != nil {
			t.Fatalf("render %s: %v", name, err)
		}
		out := buf.String()
		if !strings.Contains(out, "<!DOCTYPE html>") || !strings.Contains(out, "</html>") {
			t.Fatalf("render %s: missing page shell", name)
		}
		if strings.Count(out, `id="action-dialog"`) != 1 {
			t.Fatalf("render %s: shared action dialog missing or duplicated", name)
		}
		if strings.Contains(out, `id="error-modal"`) || strings.Contains(out, `id="confirm-modal"`) {
			t.Fatalf("render %s: legacy dialog markup remains", name)
		}
	}
}

func TestNewRequiresPageAssets(t *testing.T) {
	original := manifestData
	t.Cleanup(func() { manifestData = original })

	for _, test := range []struct {
		name     string
		manifest string
		missing  string
	}{
		{name: "empty manifest", manifest: `{}`, missing: "css/output.css"},
		{name: "missing CSS", manifest: `{"js/output.js":"test"}`, missing: "css/output.css"},
		{name: "missing JavaScript", manifest: `{"css/output.css":"test"}`, missing: "js/output.js"},
	} {
		t.Run(test.name, func(t *testing.T) {
			manifestData = []byte(test.manifest)
			_, err := New()
			if err == nil {
				t.Fatalf("New succeeded without required asset %q", test.missing)
			}
			if !strings.Contains(err.Error(), test.missing) {
				t.Fatalf("New error = %q, want missing asset %q", err, test.missing)
			}
		})
	}
}

func TestSharedDialogUsesTextContentForDynamicMessages(t *testing.T) {
	source, err := assetsFS.ReadFile("assets/js/src/ui.js")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	if !strings.Contains(text, "msgEl.textContent = message") {
		t.Fatal("shared dialog does not assign dynamic messages through textContent")
	}
	if strings.Contains(text, "msgEl.innerHTML") {
		t.Fatal("shared dialog assigns dynamic messages through innerHTML")
	}
}
