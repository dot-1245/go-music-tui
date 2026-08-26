// Package player provides the playerctl boundary used by the TUI and debug
// commands.
package player

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

// Runner is the small side-effect boundary needed by Client. Tests can provide
// a fake runner without invoking playerctl.
type Runner interface {
	Output(context.Context, ...string) ([]byte, error)
	Run(context.Context, ...string) error
}

// ExecRunner invokes an external command.
type ExecRunner struct {
	Command string
}

func (r ExecRunner) command() string {
	if strings.TrimSpace(r.Command) == "" {
		return "playerctl"
	}
	return r.Command
}

func (r ExecRunner) Output(ctx context.Context, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, r.command(), args...).Output()
}

func (r ExecRunner) Run(ctx context.Context, args ...string) error {
	return exec.CommandContext(ctx, r.command(), args...).Run()
}

// Client wraps playerctl operations.
type Client struct {
	Runner         Runner
	CommandTimeout time.Duration
}

// New creates a playerctl client. A nil runner uses the playerctl executable.
func New(runner Runner) *Client {
	return NewWithTimeout(runner, 800*time.Millisecond)
}

// NewWithTimeout creates a playerctl client with a per-command deadline. A
// zero or negative timeout leaves the caller's context as the only deadline.
func NewWithTimeout(runner Runner, timeout time.Duration) *Client {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Client{Runner: runner, CommandTimeout: timeout}
}

func (c *Client) commandContext(parent context.Context) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	if c.CommandTimeout <= 0 {
		return parent, func() {}
	}
	if deadline, ok := parent.Deadline(); ok && time.Until(deadline) <= c.CommandTimeout {
		return parent, func() {}
	}
	return context.WithTimeout(parent, c.CommandTimeout)
}

// Output executes playerctl and returns trimmed stdout.
func (c *Client) Output(ctx context.Context, args ...string) (string, error) {
	commandContext, cancel := c.commandContext(ctx)
	defer cancel()
	output, err := c.Runner.Output(commandContext, args...)
	return strings.TrimSpace(string(output)), err
}

// Execute executes playerctl and returns its error.
func (c *Client) Execute(ctx context.Context, args ...string) error {
	commandContext, cancel := c.commandContext(ctx)
	defer cancel()
	return c.Runner.Run(commandContext, args...)
}

// Control executes an operation for one player, such as next or play-pause.
func (c *Client) Control(ctx context.Context, playerName string, args ...string) error {
	command := append([]string{"-p", playerName}, args...)
	return c.Execute(ctx, command...)
}

// Query executes an arbitrary playerctl query for one player.
func (c *Client) Query(ctx context.Context, playerName string, args ...string) (string, error) {
	command := append([]string{"-p", playerName}, args...)
	return c.Output(ctx, command...)
}

// executable returns the command used by an ExecRunner. Follow requires a
// long-lived process and therefore cannot be expressed through Runner.Output.
// A fake Runner deliberately does not support Follow; this keeps the small
// command boundary easy to test.
func (c *Client) executable() string {
	switch runner := c.Runner.(type) {
	case ExecRunner:
		return runner.command()
	case *ExecRunner:
		if runner == nil {
			return ""
		}
		return runner.command()
	default:
		return ""
	}
}
