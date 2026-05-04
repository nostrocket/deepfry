// Package runner abstracts the external commands the rescuer shells out to
// (docker exec ... strfry export, docker exec ... strfry delete). The
// indirection exists so the exporter and deleter can be tested without a
// running docker daemon.
package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// Runner runs external commands.
type Runner interface {
	// Stream starts the command and returns its stdout. Closing the
	// reader sends SIGKILL if the process is still running. After
	// EOF, the caller must call Wait to surface a non-zero exit.
	Stream(ctx context.Context, name string, args ...string) (stdout io.ReadCloser, wait func() error, err error)

	// Output runs the command to completion and returns combined stdout
	// (stderr is captured into the error on failure).
	Output(ctx context.Context, name string, args ...string) (stdout []byte, err error)
}

// Exec is the production Runner. It uses os/exec.
type Exec struct{}

func (Exec) Stream(ctx context.Context, name string, args ...string) (io.ReadCloser, func() error, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start %s: %w", name, err)
	}
	wait := func() error {
		if err := cmd.Wait(); err != nil {
			return fmt.Errorf("%s exited %w; stderr=%s", name, err, stderr.String())
		}
		return nil
	}
	return out, wait, nil
}

func (Exec) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		return out, fmt.Errorf("%s failed: %w; stderr=%s", name, err, stderr.String())
	}
	return out, nil
}
