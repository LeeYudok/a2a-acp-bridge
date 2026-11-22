//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package board

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPoolAdmissionAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("A2A_POOL_FILE", filepath.Join(dir, "pool.json"))
	t.Setenv("POOL_TEST_BOARD", filepath.Join(dir, "board.json"))
	if err := os.WriteFile(os.Getenv("A2A_POOL_FILE"), []byte(`{"default":{"min":0,"max":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	start, release, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer start.Close()
	defer release.Close()

	type child struct {
		cmd    *exec.Cmd
		output *bytes.Buffer
	}
	var children []child
	for range 2 {
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPoolAdmissionProcessHelper$")
		output := new(bytes.Buffer)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = start, output, output
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, child{cmd, output})
		t.Cleanup(func() {
			if cmd.ProcessState == nil {
				_ = cmd.Process.Kill()
				_ = cmd.Wait()
			}
		})
	}
	if err := release.Close(); err != nil {
		t.Fatal(err)
	}
	var combined string
	for _, child := range children {
		if err := child.cmd.Wait(); err != nil {
			t.Errorf("process failed: %v: %s", err, child.output)
		}
		combined += child.output.String()
	}
	if strings.Count(combined, "admitted") != 1 || strings.Count(combined, "refused") != 1 {
		t.Fatalf("process results: %s", combined)
	}
}

func TestPoolAdmissionProcessHelper(t *testing.T) {
	path := os.Getenv("POOL_TEST_BOARD")
	if path == "" {
		t.Skip("subprocess helper")
	}
	if _, err := io.ReadAll(os.Stdin); err != nil {
		t.Fatal(err)
	}
	_, err := New(path).StartDelegation("user", "codex", "concurrent work", "", nil)
	switch {
	case err == nil:
		os.Stdout.WriteString("admitted\n")
	case errors.Is(err, ErrPoolFull):
		os.Stdout.WriteString("refused\n")
	default:
		t.Fatal(err)
	}
}
