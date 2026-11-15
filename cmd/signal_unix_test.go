//go:build unix

package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The first SIGINT cancels the context; a second one ends the process, also when the
// shutdown that the first one started does not finish. The test runs itself as the
// process: signalContext, then a shutdown that never ends.
func TestSecondSignalEndsTheProcess(t *testing.T) {
	if os.Getenv("MODBUS_SIMULATOR_SIGNAL_TEST") == "child" {
		ctx, stop := signalContext()
		defer stop()
		fmt.Println("ready")
		<-ctx.Done()
		fmt.Println("stopping")
		<-time.After(time.Minute) // the shutdown that does not finish
		os.Exit(0)
	}

	child := exec.Command(os.Args[0], "-test.run=^TestSecondSignalEndsTheProcess$")
	child.Env = append(os.Environ(), "MODBUS_SIMULATOR_SIGNAL_TEST=child")
	out, err := child.StdoutPipe()
	require.NoError(t, err)
	require.NoError(t, child.Start())
	defer func() { _ = child.Process.Kill() }()
	lines := bufio.NewScanner(out)
	expect := func(want string) {
		t.Helper()
		require.True(t, lines.Scan(), "the process ended before it printed %q", want)
		require.Equal(t, want, lines.Text())
	}

	expect("ready")
	require.NoError(t, child.Process.Signal(syscall.SIGINT))
	expect("stopping")

	exited := make(chan error, 1)
	go func() { exited <- child.Wait() }()
	// Until the default handling is back, a signal is still caught; send it again until the
	// process ends.
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case err := <-exited:
			var exit *exec.ExitError
			require.ErrorAs(t, err, &exit, "the process ended normally after the second signal")
			ws, ok := exit.Sys().(syscall.WaitStatus)
			require.True(t, ok)
			require.True(t, ws.Signaled(), "status %v", ws)
			require.Equal(t, syscall.SIGINT, ws.Signal())
			return
		case <-tick.C:
			_ = child.Process.Signal(syscall.SIGINT)
		case <-deadline:
			t.Fatal("a second SIGINT did not end the process within 10 s")
		}
	}
}
