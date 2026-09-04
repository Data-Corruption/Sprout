// --- FILE template ---

package cut

import (
	"bytes"
	"fmt"
	"strings"
)

type markerKind uint8

const (
	markerBegin markerKind = iota + 1
	markerEnd
	markerFile
)

func (k markerKind) String() string {
	switch k {
	case markerEnd:
		return "END"
	case markerFile:
		return "FILE"
	default:
		return "BEGIN"
	}
}

type marker struct {
	kind    markerKind
	feature string
}

type markerStyle struct {
	prefix string
	suffix string
}

var markerStyles = []markerStyle{
	{prefix: "// --- ", suffix: " ---"},
	{prefix: "# --- ", suffix: " ---"},
	{prefix: "<!-- --- ", suffix: " --- -->"},
	{prefix: "/* --- ", suffix: " --- */"},
}

func parseMarker(line []byte) (marker, bool, error) {
	text := strings.TrimSpace(string(bytes.TrimSuffix(line, []byte{'\r'})))
	for _, style := range markerStyles {
		if !strings.HasPrefix(text, style.prefix) {
			continue
		}

		body := strings.TrimPrefix(text, style.prefix)
		if !strings.HasSuffix(body, style.suffix) {
			if hasMarkerVerb(body) {
				return marker{}, false, fmt.Errorf("malformed feature marker")
			}
			continue
		}
		body = strings.TrimSuffix(body, style.suffix)
		fields := strings.Fields(body)
		if len(fields) == 0 || !isMarkerVerb(fields[0]) {
			continue
		}
		if len(fields) != 2 {
			return marker{}, false, fmt.Errorf("malformed feature marker")
		}
		if _, ok := knownOwners[fields[1]]; !ok {
			return marker{}, false, fmt.Errorf("unknown owner %q in marker", fields[1])
		}

		kind := markerBegin
		switch fields[0] {
		case "END":
			kind = markerEnd
		case "FILE":
			kind = markerFile
		}
		return marker{kind: kind, feature: fields[1]}, true, nil
	}
	return marker{}, false, nil
}

func hasMarkerVerb(body string) bool {
	fields := strings.Fields(body)
	return len(fields) > 0 && isMarkerVerb(fields[0])
}

func isMarkerVerb(word string) bool {
	switch word {
	case "BEGIN", "END", "FILE":
		return true
	default:
		return false
	}
}
