package cmd

import (
	"os"
	"testing"

	"github.com/otfabric/modbus-simulator/internal/leakcheck"
)

// No test may leave a goroutine running.
func TestMain(m *testing.M) { os.Exit(leakcheck.Main(m)) }
