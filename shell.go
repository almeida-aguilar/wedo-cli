package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"wedo-cli/internal/wedo"
)

// RunFunc executes one command. ctx is cancelled by Ctrl+C.
type RunFunc func(ctx context.Context, s *Shell, args []string) error

// Command is one entry of a menu.
type Command struct {
	Name  string
	Usage string // shown in help; defaults to Name
	Help  string
	Run   RunFunc
}

// Menu is one prompt level (hub, led, motor...).
type Menu struct {
	Name     string
	Commands []Command
	// Fallback gets lines that match no command; return handled=false to
	// let the shell report an unknown command.
	Fallback func(ctx context.Context, s *Shell, name string, args []string) (handled bool, err error)
}

var errQuit = errors.New("quit")

// Shell is the interactive prompt.
type Shell struct {
	hub   *wedo.Hub
	out   io.Writer
	stack []*Menu

	mu     sync.Mutex
	cancel context.CancelFunc
}

func newShell(h *wedo.Hub, out io.Writer) *Shell {
	return &Shell{hub: h, out: out, stack: []*Menu{hubMenu()}}
}

func (s *Shell) printf(format string, a ...any) { fmt.Fprintf(s.out, format, a...) }
func (s *Shell) println(a ...any)               { fmt.Fprintln(s.out, a...) }
func (s *Shell) cur() *Menu                     { return s.stack[len(s.stack)-1] }
func (s *Shell) push(m *Menu)                   { s.stack = append(s.stack, m) }

func (s *Shell) setCancel(c context.CancelFunc) {
	s.mu.Lock()
	s.cancel = c
	s.mu.Unlock()
}

// interrupt cancels the running command, or closes the hub and exits when
// the shell is idle at the prompt.
func (s *Shell) interrupt() {
	s.mu.Lock()
	c := s.cancel
	s.mu.Unlock()
	if c != nil {
		c()
		return
	}
	s.println("\nClosing connection...")
	_ = s.hub.Close()
	os.Exit(130)
}

// Run reads commands until exit, EOF or hub loss.
func (s *Shell) Run() {
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigc)
	go func() {
		for range sigc {
			s.interrupt()
		}
	}()

	defer s.hub.Close()
	in := bufio.NewReader(os.Stdin)
	for {
		s.printf("%s> ", s.cur().Name)
		line, err := in.ReadString('\n')
		line = strings.TrimSpace(line)
		if err != nil && line == "" {
			s.println()
			return
		}
		if line == "" {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		s.setCancel(cancel)
		derr := s.dispatch(ctx, strings.Fields(line))
		s.setCancel(nil)
		cancel()
		if errors.Is(derr, errQuit) {
			return
		}
		if derr != nil {
			s.printf("Error: %v\n", derr)
		}
		if !s.hub.Alive() {
			s.println("The hub disconnected.")
			return
		}
	}
}

func (s *Shell) dispatch(ctx context.Context, fields []string) error {
	m := s.cur()
	name := strings.ToLower(fields[0])
	args := fields[1:]
	switch name {
	case "help", "?":
		s.help(m)
		return nil
	case "exit", "quit":
		if len(s.stack) > 1 {
			s.stack = s.stack[:len(s.stack)-1]
			return nil
		}
		return s.quit()
	}
	for _, c := range m.Commands {
		if c.Name == name {
			return c.Run(ctx, s, args)
		}
	}
	if m.Fallback != nil {
		if handled, err := m.Fallback(ctx, s, name, args); handled {
			return err
		}
	}
	return fmt.Errorf("unknown command %q (type help)", fields[0])
}

func (s *Shell) quit() error {
	_ = s.hub.Close()
	return errQuit
}

func (s *Shell) help(m *Menu) {
	tw := tabwriter.NewWriter(s.out, 0, 4, 2, ' ', 0)
	for _, c := range m.Commands {
		u := c.Usage
		if u == "" {
			u = c.Name
		}
		fmt.Fprintf(tw, "  %s\t%s\n", u, c.Help)
	}
	if len(s.stack) > 1 {
		fmt.Fprintf(tw, "  exit\tReturn to %s\n", s.stack[len(s.stack)-2].Name)
	} else {
		fmt.Fprintf(tw, "  exit\tClose hub and exit CLI\n")
	}
	tw.Flush()
}

// pump prints events (one line each, timestamped) until Ctrl+C.
func (s *Shell) pump(ctx context.Context, ch <-chan wedo.Event, line func(wedo.Event) (string, bool)) error {
	s.println("(Ctrl+C to exit)")
	for {
		select {
		case ev := <-ch:
			if ev.Kind == wedo.EvDisconnect {
				s.println("The hub disconnected.")
				return wedo.ErrDisconnected
			}
			if l, ok := line(ev); ok {
				s.printf("[%s] %s\n", ev.Time.Format("15:04:05.000"), l)
			}
		case <-ctx.Done():
			s.println()
			return nil
		}
	}
}

func enableAll(s *Shell, refs ...wedo.CharRef) {
	for _, r := range refs {
		if err := s.hub.EnableNotify(r); err != nil {
			s.printf("Warning: %v\n", err)
		}
	}
}

func parseInt(s string, lo, hi int, what string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %q", what, s)
	}
	if n < lo || n > hi {
		return 0, fmt.Errorf("%s out of range (%d..%d): %d", what, lo, hi, n)
	}
	return n, nil
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func ascii(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 32 && c < 127 {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

func enter(mk func() *Menu) RunFunc {
	return func(_ context.Context, s *Shell, _ []string) error {
		s.push(mk())
		return nil
	}
}
