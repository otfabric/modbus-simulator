package main

import (
	"os"
	"testing"
)

// main runs the command line and hands its exit code to the process.
func TestMain_exitCode(t *testing.T) {
	saved, savedExit := os.Args, exit
	defer func() { os.Args, exit = saved, savedExit }()
	for _, tc := range []struct {
		args []string
		want bool // exit code 0
	}{
		{[]string{"modbus-simulator", "--help"}, true},
		{[]string{"modbus-simulator", "no-such-command"}, false},
	} {
		code := -1
		exit = func(c int) { code = c }
		os.Args = tc.args
		main()
		if (code == 0) != tc.want || code == -1 {
			t.Errorf("%v: exit %d", tc.args, code)
		}
	}
}
