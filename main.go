package main

import (
	"os"

	"github.com/Novirmor/homonto/internal/cli"
	"github.com/Novirmor/homonto/internal/fsutil"
)

func main() {
	// Resolve a symlinked working directory before any path is read; a
	// failure here surfaces from the command that needs the path.
	_ = fsutil.UseRealWorkingDirectory()
	os.Exit(cli.Execute(os.Args[1:]))
}
