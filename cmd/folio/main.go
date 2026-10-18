// SPDX-License-Identifier: AGPL-3.0-only

// Command folio is the Folio document service: `serve` (default) runs the MCP
// service, `render` renders offline from a JSON request, `healthcheck` probes
// the local /healthz, `sandbox-exec` is the internal sandbox helper and
// `version` prints the version.
package main

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/vocdoni/folio/internal/sandbox"
)

// buildVersion is set at link time by release builds
// (-ldflags "-X main.buildVersion=v1.2.3"); empty means "use the module
// build info".
var buildVersion string

func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "devel"
}

func usage() {
	fmt.Fprint(os.Stderr, `usage: folio <command> [flags]

commands:
  render        render a document from a JSON request file (no database)
  healthcheck   probe the local /healthz (exit 0 when healthy; for container health checks)
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
	case "healthcheck":
		os.Exit(healthcheckCmd(args))
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
