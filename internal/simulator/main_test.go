package simulator

import (
	"os"
	"testing"

	"github.com/otfabric/modbus-simulator/internal/leakcheck"
)

// No test may leave a goroutine running: the simulators it started, their connections
// and the clients included.
func TestMain(m *testing.M) { os.Exit(leakcheck.Main(m)) }
