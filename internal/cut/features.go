// --- FILE template ---

package cut

import (
	"fmt"
	"strings"
)

const templateOwner = "template"

var featureNames = []string{
	"update",
	"update.self",
	"update.notifications",
	"update.auto",
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

// ParseCuts validates names and expands each parent to its dotted descendants.
func ParseCuts(names []string) (Set, error) {
	if len(names) == 0 {
		return nil, fmt.Errorf("at least one feature is required")
	}

	cuts := make(Set)
	for _, name := range names {
		if _, ok := knownFeatures[name]; !ok {
			return nil, fmt.Errorf("unknown feature %q (valid features: %s)", name, strings.Join(featureNames, ", "))
		}
		for _, candidate := range featureNames {
			if candidate == name || strings.HasPrefix(candidate, name+".") {
				cuts[candidate] = struct{}{}
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
