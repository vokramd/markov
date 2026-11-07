package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"

	"github.com/vokramd/markov/pkg/outputlimit"
)

// runCapped runs cmd with its stdout and stderr counted together against the step's output limit
// (outputlimit.Limit(ctx)). Past the limit the process is killed, so a runaway command stops
// instead of filling memory, and the limit error is returned.
func runCapped(ctx context.Context, cmd *exec.Cmd) (stdout, stderr string, err error) {
	return runCappedLive(ctx, cmd, false)
}

// runCappedLive is runCapped that, when live is set, also reports each line of stdout and stderr
// as an `output` progress event (stream, text) as it is written.
func runCappedLive(ctx context.Context, cmd *exec.Cmd, live bool) (stdout, stderr string, err error) {
	counter := outputlimit.NewCounter(outputlimit.Limit(ctx))
	var out, errOut bytes.Buffer
	kill := func() {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
	}
	cmd.Stdout = counter.Writer(&out, "stdout", kill)
	cmd.Stderr = counter.Writer(&errOut, "stderr", kill)
	if live {
		outLines, errLines := newProgressLines(ctx, "stdout"), newProgressLines(ctx, "stderr")
		defer outLines.Flush()
		defer errLines.Flush()
		cmd.Stdout = io.MultiWriter(cmd.Stdout, outLines)
		cmd.Stderr = io.MultiWriter(cmd.Stderr, errLines)
	}
	err = cmd.Run()
	if lim := counter.Err(); lim != nil {
		return out.String(), errOut.String(), lim
	}
	return out.String(), errOut.String(), err
}

// readCapped reads r to the end, counting it against counter as stream; past the limit it stops
// reading and returns the limit error.
func readCapped(r io.Reader, counter *outputlimit.Counter, stream string) ([]byte, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 32*1024)
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			if lim := counter.Add(stream, n); lim != nil {
				return nil, lim
			}
			buf.Write(chunk[:n])
		}
		if err == io.EOF {
			return buf.Bytes(), nil
		}
		if err != nil {
			return buf.Bytes(), err
		}
	}
}

type counterKey struct{}

// withCounter shares one counter between the reads that make one output (every page of a
// listing).
func withCounter(ctx context.Context) (context.Context, *outputlimit.Counter) {
	if c, ok := ctx.Value(counterKey{}).(*outputlimit.Counter); ok {
		return ctx, c
	}
	c := outputlimit.NewCounter(outputlimit.Limit(ctx))
	return context.WithValue(ctx, counterKey{}, c), c
}

// limitErr wraps an executor's limit error with its name, keeping it findable with
// outputlimit.As.
func limitErr(name string, err error) error {
	return fmt.Errorf("%s: %w", name, err)
}

// liveOutputParam reads the optional boolean live_output param of a command step.
func liveOutputParam(raw any, step string) (bool, error) {
	if raw == nil {
		return false, nil
	}
	live, ok := raw.(bool)
	if !ok {
		return false, fmt.Errorf("%s: live_output must be a boolean", step)
	}
	return live, nil
}
