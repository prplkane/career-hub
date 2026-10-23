//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os/exec"
	"time"
)

// Bound both pipes; do not embed bytes.Buffer (io.Copy would bypass Write).
type migrationFixtureOutput struct {
	data     []byte
	limit    int
	overflow bool
}

func (b *migrationFixtureOutput) Write(raw []byte) (int, error) {
	if len(raw) > b.limit-len(b.data) {
		b.overflow = true
		return 0, io.ErrShortWrite
	}
	b.data = append(b.data, raw...)
	return len(raw), nil
}
func runBoundedMigrationFixture(executable string, args []string, dir string, input []byte, limit int) ([]byte, []byte, int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(append(bytes.Clone(input), '\n'))
	cmd.WaitDelay = time.Second
	out, stderr := &migrationFixtureOutput{limit: limit}, &migrationFixtureOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	var exited *exec.ExitError
	if ctx.Err() != nil || out.overflow || stderr.overflow || err != nil && !errors.As(err, &exited) || cmd.ProcessState == nil {
		return nil, nil, -1, errors.New("bounded fixture process failed")
	}
	return out.data, stderr.data, cmd.ProcessState.ExitCode(), nil
}
