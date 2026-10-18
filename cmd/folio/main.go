// SPDX-License-Identifier: AGPL-3.0-only

// Command folio is the Folio document service. P1 provides the `render`,
// `sandbox-exec` and `version` subcommands; `serve` arrives with the MCP layer.
package main

import (
	"fmt"
	"os"
	"runtime/debug"
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
  serve         run the MCP service (not available in this build)
`)
}

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}
	switch cmd {
	case "version":
		fmt.Println("folio", version())
	case "serve":
		fmt.Fprintln(os.Stderr, "folio: serve is not available in this build yet; use `folio render`")
		os.Exit(2)
	case "-h", "--help", "help":
		usage()
	default:
		usage()
		os.Exit(2)
	}
}
