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

func main() {
	cmd.SetBuildMeta(version, tag, commit, buildDate)
	os.Exit(cmd.Execute())
}
