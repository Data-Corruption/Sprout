// --- FILE service.https ---

package types

import (
	"fmt"
	"strings"
)

// Perm is a permission bitmask attached to credentials and sessions. This is
// a small starter set - add app-specific bits above PermAdmin as needed (the
// pattern scales to 64 bits).
type Perm uint64

const (
	PermSettings      Perm = 1 << iota // settings
	PermServerControl                  // server.control

	PermAdmin Perm = ^Perm(0)
)

var permNames = [...]struct {
	name string
	perm Perm
}{
	{"settings", PermSettings},
	{"server.control", PermServerControl},
}

var PermNames map[string]Perm

func init() {
	PermNames = make(map[string]Perm, len(permNames)+1)
	for _, e := range permNames {
		PermNames[e.name] = e.perm
	}
	PermNames["admin"] = PermAdmin
}

func (p Perm) Has(required Perm) bool {
	return p&required == required
}

func (p Perm) String() string {
	if p == PermAdmin {
		return "admin"
	}
	var parts []string
	for _, e := range permNames {
		if p&e.perm != 0 {
			parts = append(parts, e.name)
		}
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, " ")
}

// ParsePerms parses a space-separated permission spec. Tokens are OR'd in;
// a leading '!' clears the bit instead. Example: "admin !server.control"
// yields all permissions except PermServerControl.
func ParsePerms(args []string) (Perm, error) {
	var p Perm
	for _, tok := range args {
		neg := false
		name := tok
		if strings.HasPrefix(tok, "!") {
			neg = true
			name = tok[1:]
		}
		bit, ok := PermNames[name]
		if !ok {
			return 0, fmt.Errorf("unknown permission %q", name)
		}
		if neg {
			p &^= bit
		} else {
			p |= bit
		}
	}
	return p, nil
}
