// Command gh-board is a GitHub CLI extension for running an issue board:
// filing issues, checking them ready, claiming them and moving them along.
package main

import (
	"errors"
	"fmt"
	"os"
	"runtime/debug"

	"github.com/alecthomas/kong"
	"github.com/dan-online/gh-board/internal/board"
)

// version is set at release time with -ldflags=-X=main.version=vX.Y.Z.
var version = ""

func main() {
	if version == "" {
		version = "(devel)"
		if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
			version = info.Main.Version
		}
	}
	var cli CLI
	parser, err := kong.New(&cli,
		kong.Name("gh board"),
		kong.Description("Run an issue board on a GitHub project: file, ready, claim and move issues.\n\n"+
			"Reads .github/board.yml from the current checkout. Needs a gh token with the project scope, "+
			"write access to the project, and git push access to the board's repo. Run gh board skill for the full guide."),
		kong.Vars{"version": version},
		kong.Exit(func(code int) { os.Exit(code) }),
	)
	if err != nil {
		panic(err)
	}
	args := os.Args[1:]
	if len(args) == 0 {
		args = []string{"--help"}
	}
	ctx, err := parser.Parse(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(board.ExitUsage)
	}
	if err := ctx.Run(&env{version: version, model: parser.Model}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		var be *board.Error
		if errors.As(err, &be) {
			os.Exit(be.Code)
		}
		os.Exit(1)
	}
}
