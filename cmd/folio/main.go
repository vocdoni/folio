// SPDX-License-Identifier: AGPL-3.0-only

// Command folio is the Folio document service: `serve` (default) runs the MCP
// service, `render` renders offline from a JSON request, `sandbox-exec` is the
// internal sandbox helper and `version` prints the version.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/vocdoni/folio/internal/sandbox"
)

func version() string {
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "devel"
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: folio <command> [flags]

commands:
  render        render a document from a JSON request file (no database)
  version       print the version
  sandbox-exec  internal: run a tool under rlimits and Landlock
  serve         run the MCP service (default; configured with FOLIO_* variables)
`)
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "sandbox-exec":
		os.Exit(sandbox.Main(args))
	case "render":
		os.Exit(renderCmd(args))
	case "version":
		fmt.Println("folio", version())
	case "serve":
		os.Exit(serveCmd(args))
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}
