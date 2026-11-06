package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wedo-cli/internal/wedo"
)

func rawMenu() *Menu {
	return &Menu{Name: "raw", Commands: []Command{
		{Name: "gatt", Help: "Probe known characteristics: presence and values", Run: cmdGatt},
		{Name: "read", Usage: "read [uuid]", Help: "Read a characteristic (e.g. 2a19, 1526, or full UUID)", Run: cmdRawRead},
		{Name: "write", Usage: "write [uuid] [hex...] [nr]", Help: "Write bytes; nr = without response", Run: cmdRawWrite},
		{Name: "notify", Usage: "notify [uuid]", Help: "Show raw notifications (Ctrl+C to exit)", Run: cmdRawNotify},
		{Name: "input", Usage: "input [hex...]", Help: "Write to Input Command (1563)", Run: rawCommand(wedo.CharInputCmd)},
		{Name: "output", Usage: "output [hex...]", Help: "Write to Output Command (1565)", Run: rawCommand(wedo.CharOutputCmd)},
	}}
}

func shortUUID(u string) string {
	if len(u) >= 8 {
		return u[4:8]
	}
	return u
}

func cmdGatt(_ context.Context, s *Shell, _ []string) error {
	s.printf("Services: %s\n", strings.Join(s.hub.Services(), ", "))
	for _, r := range s.hub.Probe() {
		name, id := r.Ref.Name, shortUUID(r.Ref.Char)
		switch {
		case !r.Present:
			s.printf("  --  %-20s %s\n", name, id)
		case r.Err != nil:
			s.printf("  ok  %-20s %s  (read: %v)\n", name, id, r.Err)
		case r.Value != nil:
			s.printf("  ok  %-20s %s  = %s  |%s|\n", name, id, wedo.HexString(r.Value), ascii(r.Value))
		default:
			s.printf("  ok  %-20s %s\n", name, id)
		}
	}
	return nil
}

func cmdRawRead(_ context.Context, s *Shell, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: read [uuid]")
	}
	b, err := s.hub.ReadRaw(args[0])
	if err != nil {
		return err
	}
	s.printf("%s  |%s|\n", wedo.HexString(b), ascii(b))
	return nil
}

func cmdRawWrite(_ context.Context, s *Shell, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: write [uuid] [hex...] [nr]")
	}
	withResp := true
	rest := args[1:]
	if last := strings.ToLower(rest[len(rest)-1]); last == "nr" || last == "--no-response" {
		withResp = false
		rest = rest[:len(rest)-1]
	}
	data, err := wedo.ParseHex(strings.Join(rest, " "))
	if err != nil {
		return err
	}
	if err := s.hub.WriteRaw(args[0], data, withResp); err != nil {
		return err
	}
	s.printf("Written to %s: %s\n", args[0], wedo.HexString(data))
	return nil
}

func rawCommand(ref wedo.CharRef) RunFunc {
	return func(_ context.Context, s *Shell, args []string) error {
		data, err := wedo.ParseHex(strings.Join(args, " "))
		if err != nil {
			return err
		}
		if err := s.hub.Write(ref, data, true); err != nil {
			return err
		}
		s.printf("Written to %s: %s\n", ref.Name, wedo.HexString(data))
		return nil
	}
}

func cmdRawNotify(ctx context.Context, s *Shell, args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: notify [uuid]")
	}
	s.println("(Ctrl+C to exit)")
	return s.hub.RawNotify(ctx, args[0], func(b []byte) {
		s.printf("[%s] %s\n", time.Now().Format("15:04:05.000"), wedo.HexString(b))
	})
}
