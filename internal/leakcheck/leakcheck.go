// Package leakcheck finds goroutines that tests leave running. It compares the goroutines
// running before and after, by id, and reports the stack of each one left behind. It is
// used by the tests of this repository only.
package leakcheck

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// checkTimeout is how long Check and Main wait for goroutines that are ending.
var checkTimeout = 5 * time.Second

// Snapshot is the set of goroutines running at one moment, by id.
type Snapshot map[uint64]struct{}

// Take returns the goroutines running now.
func Take() Snapshot {
	snap := Snapshot{}
	for _, g := range stacks() {
		snap[g.id] = struct{}{}
	}
	return snap
}

type goroutine struct {
	id    uint64
	stack string
}

// stacks returns every goroutine of the program but the calling one.
func stacks() []goroutine {
	buf := make([]byte, 1<<16)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	var out []goroutine
	// The first block is the calling goroutine.
	for i, block := range bytes.Split(buf, []byte("\n\n")) {
		if i == 0 {
			continue
		}
		header, _, _ := strings.Cut(string(block), "\n")
		// "goroutine 42 [chan receive]:"
		fields := strings.Fields(header)
		if len(fields) < 2 || fields[0] != "goroutine" {
			continue
		}
		id, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			continue
		}
		out = append(out, goroutine{id: id, stack: string(block)})
	}
	return out
}

// Leaked waits up to timeout for the goroutines that are not in before to end, and
// returns the stacks of those still running, or "" when none is left.
func Leaked(before Snapshot, timeout time.Duration) string {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		var left []string
		for _, g := range stacks() {
			if _, ok := before[g.id]; !ok {
				left = append(left, g.stack)
			}
		}
		if len(left) == 0 {
			return ""
		}
		select {
		case <-deadline.C:
			return fmt.Sprintf("%d goroutine(s) still running:\n\n%s", len(left), strings.Join(left, "\n\n"))
		case <-tick.C:
		}
	}
}

// TB is the part of testing.TB that Check uses.
type TB interface {
	Helper()
	Errorf(format string, args ...any)
}

// Check fails t when goroutines started since before are still running 5 s later.
func Check(t TB, before Snapshot) {
	t.Helper()
	if leaked := Leaked(before, checkTimeout); leaked != "" {
		t.Errorf("goroutine leak: %s", leaked)
	}
}

// M is the part of testing.M that Main uses.
type M interface {
	Run() int
}

// Main runs the tests of a package and fails the run when goroutines that they started
// are still running afterwards. Call it from TestMain: os.Exit(leakcheck.Main(m)). It does
// not check a run that failed, and leaves fuzzing alone.
func Main(m M) int {
	before := Take()
	code := m.Run()
	if code != 0 || fuzzing() {
		return code
	}
	if leaked := Leaked(before, checkTimeout); leaked != "" {
		_, _ = fmt.Fprintf(os.Stderr, "goroutine leak after the tests: %s\n", leaked)
		return 1
	}
	return code
}

// fuzzing reports whether the test binary runs a fuzz target, as coordinator or worker.
func fuzzing() bool {
	for _, name := range []string{"test.fuzz", "test.fuzzworker"} {
		if f := flag.Lookup(name); f != nil && f.Value.String() != "" && f.Value.String() != "false" {
			return true
		}
	}
	return false
}
