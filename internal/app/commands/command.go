// Package commands provides CLI command definitions for the application.
//
// Like a few other packages in this project, It uses a plugin style registration system
// simply because I like the pattern. If i need better go vet support later, swapping to
// a static list is trivial.
package commands

import (
	"sprout/internal/app"

	"github.com/urfave/cli/v3"
)

type RegFunc func(a *app.App) *cli.Command

var Registry []RegFunc

func register(rf RegFunc) RegFunc {
	if rf != nil {
		Registry = append(Registry, rf)
	}
	return rf
}
