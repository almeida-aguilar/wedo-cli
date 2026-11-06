package main

import (
	"context"
	"fmt"

	"wedo-cli/internal/wedo"
)

func buttonMenu() *Menu {
	return &Menu{Name: "button", Commands: []Command{
		{Name: "state", Help: "One-shot query: PRESSED / RELEASED", Run: func(_ context.Context, s *Shell, _ []string) error {
			b, err := s.hub.Read(wedo.CharButton)
			if err != nil {
				return err
			}
			if len(b) == 0 {
				return fmt.Errorf("empty read")
			}
			if b[0] != 0 {
				s.println("PRESSED")
			} else {
				s.println("RELEASED")
			}
			return nil
		}},
		{Name: "listen", Help: "Listen for PRESSED/RELEASED (Ctrl+C to exit)", Run: func(ctx context.Context, s *Shell, _ []string) error {
			ch, cancel := s.hub.Subscribe()
			defer cancel()
			enableAll(s, wedo.CharButton)
			return s.pump(ctx, ch, onlyKind(wedo.EvButton))
		}},
	}}
}
