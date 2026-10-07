// Package dockerutil shells out to `docker compose` so pgctl's lifecycle
// commands (create/scale/decommission) stay a thin wrapper around Compose
// rather than reimplementing container orchestration.
package dockerutil

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// Compose runs `docker compose -f file <args...>` and returns combined
// stdout+stderr on failure so CLI callers can surface it directly.
func Compose(ctx context.Context, file string, args ...string) (string, error) {
	full := append([]string{"compose", "-f", file}, args...)
	cmd := exec.CommandContext(ctx, "docker", full...)

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("docker %v: %w\n%s", full, err, out.String())
	}
	return out.String(), nil
}

// Up starts the given services (or all services if none are named) detached.
func Up(ctx context.Context, file string, services ...string) (string, error) {
	args := append([]string{"up", "-d"}, services...)
	return Compose(ctx, file, args...)
}

// Down stops and removes the stack, including volumes — a full teardown for
// `pgctl decommission`.
func Down(ctx context.Context, file string) (string, error) {
	return Compose(ctx, file, "down", "-v")
}

// Scale sets the replica count for a single service, e.g. adding a read
// replica.
func Scale(ctx context.Context, file, service string, replicas int) (string, error) {
	return Compose(ctx, file, "up", "-d", "--scale", fmt.Sprintf("%s=%d", service, replicas), service)
}
