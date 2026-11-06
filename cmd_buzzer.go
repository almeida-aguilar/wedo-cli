package main

import (
	"context"
	"fmt"
	"strconv"
)

// The piezo has no built-in tone table: these 16 are plain notes, edit
// freely. Index 1 is an A, as in the spec example.
var tones = []struct {
	Note string
	Freq uint16
}{
	{"A3", 220}, {"B3", 247}, {"C4", 262}, {"D4", 294},
	{"E4", 330}, {"F4", 349}, {"G4", 392}, {"A4", 440},
	{"B4", 494}, {"C5", 523}, {"D5", 587}, {"E5", 659},
	{"F5", 698}, {"G5", 784}, {"A5", 880}, {"B5", 988},
}

const toneMillis = 500

func buzzerMenu() *Menu {
	return &Menu{
		Name: "buzzer",
		Commands: []Command{
			{Name: "tone", Usage: "tone [freq] [ms]", Help: "Frequency 0-1500 Hz for [ms]", Run: cmdTone},
			{Name: "off", Help: "Stop sound", Run: func(_ context.Context, s *Shell, _ []string) error {
				if err := s.hub.StopTone(); err != nil {
					return err
				}
				s.println("Sound stopped")
				return nil
			}},
			{Name: "list", Help: "Show table of 16 tones", Run: func(_ context.Context, s *Shell, _ []string) error {
				for i, t := range tones {
					s.printf("%2d  %-3s %4d Hz\n", i+1, t.Note, t.Freq)
				}
				return nil
			}},
		},
		Fallback: func(_ context.Context, s *Shell, name string, _ []string) (bool, error) {
			n, err := strconv.Atoi(name)
			if err != nil {
				return false, nil
			}
			if n < 1 || n > len(tones) {
				return true, fmt.Errorf("tone out of range (1..%d)", len(tones))
			}
			t := tones[n-1]
			if err := s.hub.Tone(t.Freq, toneMillis); err != nil {
				return true, err
			}
			s.printf("Tone %d (%s)\n", n, t.Note)
			return true, nil
		},
	}
}

func cmdTone(_ context.Context, s *Shell, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: tone [freq] [ms]")
	}
	freq, err := parseInt(args[0], 0, 1500, "frequency")
	if err != nil {
		return err
	}
	ms, err := parseInt(args[1], 1, 65535, "ms")
	if err != nil {
		return err
	}
	if err := s.hub.Tone(uint16(freq), uint16(ms)); err != nil {
		return err
	}
	s.printf("Tone %d Hz for %d ms\n", freq, ms)
	return nil
}
