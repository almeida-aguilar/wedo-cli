package main

import (
	"context"
	"fmt"
)

// Predefined colour indices (LEGO colour table; 0 = off).
var ledColorNames = []string{
	"off", "pink", "purple", "blue", "sky blue", "teal",
	"green", "yellow", "orange", "red", "white",
}

func ledMenu() *Menu {
	return &Menu{Name: "led", Commands: []Command{
		{Name: "on", Help: "Turn on white", Run: func(_ context.Context, s *Shell, _ []string) error {
			if err := s.hub.LEDRGB(255, 255, 255); err != nil {
				return err
			}
			s.println("LED turned on (white)")
			return nil
		}},
		{Name: "off", Help: "Turn off LED", Run: func(_ context.Context, s *Shell, _ []string) error {
			if err := s.hub.LEDRGB(0, 0, 0); err != nil {
				return err
			}
			s.println("LED turned off")
			return nil
		}},
		{Name: "color", Usage: "color [index] | color [r] [g] [b]", Help: "Index 0-10, or absolute RGB with each channel 0-255", Run: cmdLEDColor},
		{Name: "list", Help: "Table of colours by index (extra)", Run: func(_ context.Context, s *Shell, _ []string) error {
			for i, n := range ledColorNames {
				s.printf("%2d  %s\n", i, n)
			}
			return nil
		}},
	}}
}

func cmdLEDColor(_ context.Context, s *Shell, args []string) error {
	switch len(args) {
	case 1:
		i, err := parseInt(args[0], 0, len(ledColorNames)-1, "index")
		if err != nil {
			return err
		}
		if err := s.hub.LEDIndex(byte(i)); err != nil {
			return err
		}
		s.printf("LED set to %s (index %d)\n", ledColorNames[i], i)
		return nil
	case 3:
		var c [3]int
		for k, name := range []string{"red", "green", "blue"} {
			v, err := parseInt(args[k], 0, 255, name)
			if err != nil {
				return err
			}
			c[k] = v
		}
		if err := s.hub.LEDRGB(byte(c[0]), byte(c[1]), byte(c[2])); err != nil {
			return err
		}
		s.printf("LED set to RGB (%d, %d, %d)\n", c[0], c[1], c[2])
		return nil
	}
	return fmt.Errorf("usage: color [index]  |  color [r] [g] [b]")
}
