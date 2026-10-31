package main

import (
	"os"

	"github.com/otfabric/modbus-simulator/cmd"
)

// Injected at link time (see build tooling): -X main.version=... etc.
var (
	version   = "dev"
	tag       = ""
	commit    = ""
	buildDate = ""
)

// exit is os.Exit, replaced by the test of main.
var exit = os.Exit

func main() {
	cmd.SetBuildMeta(version, tag, commit, buildDate)
	exit(cmd.Execute())
}
