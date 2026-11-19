//go:build !windows

package cmd

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestSignalHelper is the process that TestSecondSignalTerminates starts. It is
// not a test when the variable is not set.
func TestSignalHelper(t *testing.T) {
	if os.Getenv("D2N_SIGNAL_HELPER") != "1" {
		t.Skip("helper process")
	}
	ctx, stop := notifyContext(context.Background())
	defer stop()
	os.Stdout.WriteString("ready\n")
	<-ctx.Done()
	os.Stdout.WriteString("first\n")
	// The cleanup is stuck: only a second signal can end the process.
	time.Sleep(30 * time.Second)
	os.Stdout.WriteString("not terminated\n")
}

func TestSecondSignalTerminates(t *testing.T) {
	for _, first := range []syscall.Signal{syscall.SIGINT, syscall.SIGHUP, syscall.SIGTERM} {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSignalHelper$")
		cmd.Env = append(os.Environ(), "D2N_SIGNAL_HELPER=1")
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		r := bufio.NewReader(out)
		waitLine := func(want string) {
			t.Helper()
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					t.Fatalf("%v: waiting for %q: %v", first, want, err)
				}
				if strings.TrimSpace(l) == want {
					return
				}
			}
		}
		waitLine("ready")
		cmd.Process.Signal(first)
		waitLine("first")
		start := time.Now()
		cmd.Process.Signal(syscall.SIGINT)
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err == nil {
				t.Errorf("%v: the helper exited normally", first)
			}
			if time.Since(start) > 10*time.Second {
				t.Errorf("%v: slow exit", first)
			}
		case <-time.After(15 * time.Second):
			cmd.Process.Kill()
			t.Fatalf("%v: a second signal did not end the process", first)
		}
	}
}
