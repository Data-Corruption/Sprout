// --- FILE template ---

package cut

import (
	"fmt"
	"strings"
)

const templateOwner = "template"

var featureNames = []string{
	"update",
	"update.apply",
	"update.apply.auto",
	"service",
	"service.https",
}

var knownFeatures = func() map[string]struct{} {
	features := make(map[string]struct{}, len(featureNames))
	for _, name := range featureNames {
		features[name] = struct{}{}
	}
	return features
}()

var knownOwners = func() map[string]struct{} {
	owners := make(map[string]struct{}, len(knownFeatures)+1)
	for name := range knownFeatures {
		owners[name] = struct{}{}
	}
	owners[templateOwner] = struct{}{}
	return owners
}()

// Set is a validated set of canonical features to remove.
type Set map[string]struct{}

// Features returns the canonical feature names in display order.
func Features() []string {
	return append([]string(nil), featureNames...)
}

// Contract is the discovery protocol used by transplant before it edits a tree.
// Bump Version when the CLI, project-block, or docs-layout contract changes.
type Contract struct {
	Version  int       `json:"version"`
	Features []Feature `json:"features"`
}

type Feature struct {
	Name          string   `json:"name"`
	Prerequisites []string `json:"prerequisites"`
}

func FeatureContract() Contract {
	contract := Contract{Version: 1}
	for _, name := range Features() {
		contract.Features = append(contract.Features, Feature{
			Name:          name,
			Prerequisites: append([]string{}, Prerequisites(name)...),
		})
	}
	return contract
}

// Prerequisites returns the features required by name. This is the dependency
// graph used by the cutter, its preview, and the source-shape matrix.
func Prerequisites(name string) []string {
	switch name {
	case "update.apply":
		return []string{"update"}
	case "update.apply.auto":
		return []string{"update.apply", "service"}
	case "service.https":
		return []string{"service"}
	default:
		return nil
	}
}

// ParseCuts validates names and transitively removes dependent features.
func ParseCuts(names []string) (Set, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one feature is required")
	}

	cuts := make(Set)
	for _, name := range names {
		if _, ok := knownFeatures[name]; !ok {
			return nil, fmt.Errorf("unknown feature %q (valid features: %s)", name, strings.Join(featureNames, ", "))
		}
		cuts[name] = struct{}{}
	}
	for changed := true; changed; {
		changed = false
		for _, name := range featureNames {
			if _, removed := cuts[name]; removed {
				continue
			}
			for _, prerequisite := range Prerequisites(name) {
				if _, removed := cuts[prerequisite]; removed {
					cuts[name] = struct{}{}
					changed = true
					break
				}
			}
		}
	}
	return cuts, nil
}

func validateCuts(cuts Set) error {
	if len(cuts) == 0 {
		return fmt.Errorf("at least one feature is required")
	}
	for name := range cuts {
		if _, ok := knownFeatures[name]; !ok {
			return fmt.Errorf("unknown feature %q", name)
		}
	}
	return nil
}

// Variant is one distinct retained feature set. Both CI platforms consume the
// same enumeration so a newly declared dependency cannot leave a stale matrix.
type Variant struct {
	Name  string
	Cuts  []string
	HTTPS bool
}

func Variants() []Variant {
	var variants []Variant
	seen := make(map[string]bool)
	for mask := 0; mask < 1<<len(featureNames); mask++ {
		var names []string
		for i, name := range featureNames {
			if mask&(1<<i) != 0 {
				names = append(names, name)
			}
		}
		cuts := make(Set)
		if len(names) != 0 {
			cuts, _ = ParseCuts(names)
		}
		removed := make([]string, 0, len(cuts))
		for _, name := range featureNames {
			if _, ok := cuts[name]; ok {
				removed = append(removed, name)
			}
		}
		key := strings.Join(removed, ",")
		if seen[key] {
			continue
		}
		seen[key] = true
		has := func(name string) bool { _, removed := cuts[name]; return !removed }
		update := "automatic"
		switch {
		case !has("update"):
			update = "no-update"
		case !has("update.apply"):
			update = "check"
		case !has("update.apply.auto"):
			update = "manual"
		}
		service := "https-service"
		switch {
		case !has("service"):
			service = "no-service"
		case !has("service.https"):
			service = "headless-service"
		}
		variants = append(variants, Variant{Name: update + "-" + service, Cuts: removed, HTTPS: has("service.https")})
	}
	return variants
}
