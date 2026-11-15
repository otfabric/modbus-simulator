package leakcheck

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) { os.Exit(Main(m)) }

// recorder is a TB that keeps the failure.
type recorder struct{ failure string }

func (r *recorder) Helper() {}
func (r *recorder) Errorf(format string, args ...any) {
	r.failure = fmt.Sprintf(format, args...)
}

func parked(stop <-chan struct{}, started chan<- struct{}) {
	close(started)
	<-stop
}

// A goroutine that is still running is reported with its stack, one that ends is not.
func TestLeaked(t *testing.T) {
	before := Take()
	stop, started := make(chan struct{}), make(chan struct{})
	go parked(stop, started)
	<-started

	leaked := Leaked(before, 50*time.Millisecond)
	if !strings.Contains(leaked, "1 goroutine(s) still running") || !strings.Contains(leaked, "leakcheck.parked") {
		t.Fatalf("the running goroutine is not reported with its stack:\n%s", leaked)
	}
	r := &recorder{}
	time.AfterFunc(20*time.Millisecond, func() { close(stop) })
	Check(r, before)
	if r.failure != "" {
		t.Fatalf("a goroutine that ended is reported: %s", r.failure)
	}
	if leaked := Leaked(before, 0); leaked != "" {
		t.Fatalf("nothing is running, but: %s", leaked)
	}
}

func TestCheckReportsLeak(t *testing.T) {
	before := Take()
	stop, started := make(chan struct{}), make(chan struct{})
	defer close(stop)
	go parked(stop, started)
	<-started
	r := &recorder{}
	saved := checkTimeout
	checkTimeout = 20 * time.Millisecond
	defer func() { checkTimeout = saved }()
	Check(r, before)
	if !strings.Contains(r.failure, "goroutine leak") {
		t.Fatalf("leak not reported: %q", r.failure)
	}
}

type fakeM struct {
	code int
	run  func()
}

func (m fakeM) Run() int {
	if m.run != nil {
		m.run()
	}
	return m.code
}

func TestMainResult(t *testing.T) {
	saved := checkTimeout
	checkTimeout = 20 * time.Millisecond
	defer func() { checkTimeout = saved }()

	if code := Main(fakeM{code: 0}); code != 0 {
		t.Fatalf("clean run: exit %d", code)
	}
	if code := Main(fakeM{code: 3}); code != 3 {
		t.Fatalf("failed run: exit %d, want its own code", code)
	}
	stop := make(chan struct{})
	defer close(stop)
	code := Main(fakeM{run: func() {
		started := make(chan struct{})
		go parked(stop, started)
		<-started
	}})
	if code != 1 {
		t.Fatalf("run that leaves a goroutine: exit %d, want 1", code)
	}
}
