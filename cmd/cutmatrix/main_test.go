// --- FILE template ---

package main

import "testing"

func TestCanonicalVariantsCoverDependencyGraph(t *testing.T) {
	variants := canonicalVariants()
	if len(variants) != 11 {
		t.Fatalf("variant count = %d, want 11", len(variants))
	}
	names := make(map[string]bool, len(variants))
	httpsCount := 0
	for _, variant := range variants {
		if names[variant.name] {
			t.Fatalf("duplicate variant %q", variant.name)
		}
		names[variant.name] = true
		if variant.hasHTTPS {
			httpsCount++
		}
	}
	if httpsCount != 4 {
		t.Fatalf("HTTPS variant count = %d, want 4", httpsCount)
	}
}
