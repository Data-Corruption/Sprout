// --- FILE template ---

package main

import "testing"

func TestCanonicalVariantsCoverSixByThreeMatrix(t *testing.T) {
	variants := canonicalVariants()
	if len(variants) != 18 {
		t.Fatalf("variant count = %d, want 18", len(variants))
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
	if httpsCount != 6 {
		t.Fatalf("HTTPS variant count = %d, want 6", httpsCount)
	}
}
