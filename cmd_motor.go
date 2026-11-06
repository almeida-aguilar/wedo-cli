package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wedo-cli/internal/wedo"
)

func motorMenu() *Menu {
	return &Menu{Name: "motor", Commands: []Command{
		motorCmd("a", "A", []byte{wedo.ConnPortA}),
		motorCmd("b", "B", []byte{wedo.ConnPortB}),
		motorCmd("both", "both", []byte{wedo.ConnPortA, wedo.ConnPortB}),
	}}
}

// motorTargets keeps only the requested ports that really hold a motor.
func motorTargets(s *Shell, conns []byte) ([]byte, error) {
	ports := s.hub.Ports()
	var out []byte
	for _, c := range conns {
		if ev, ok := ports[c]; ok && ev.Type == wedo.TypeMotor {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		if len(conns) == 1 {
			return nil, fmt.Errorf("no motor on port %s", wedo.PortLabel(conns[0]))
		}
		return nil, fmt.Errorf("no motors connected")
	}
	return out, nil
}

func describeTargets(c []byte) string {
	if len(c) == 1 {
		return "motor " + wedo.PortLabel(c[0])
	}
	return "motors A and B"
}

func motorCmd(name, label string, conns []byte) Command {
	usage := label + " [speed] | stop | brake | run-for [ms] [speed]"
	return Command{
		Name:  name,
		Usage: usage,
		Help:  "Speed -100..100 (below ~30 the motor usually does not move)",
		Run: func(ctx context.Context, s *Shell, args []string) error {
			targets, err := motorTargets(s, conns)
			if err != nil {
				return err
			}
			desc := describeTargets(targets)
			if len(args) == 0 {
				return fmt.Errorf("usage: %s", usage)
			}
			setAll := func(speed int) error {
				for _, c := range targets {
					if err := s.hub.SetMotor(c, speed); err != nil {
						return err
					}
				}
				return nil
			}
			switch strings.ToLower(args[0]) {
			case "stop":
				if err := setAll(0); err != nil {
					return err
				}
				s.printf("OK: %s stopped\n", desc)
			case "brake":
				for _, c := range targets {
					if err := s.hub.BrakeMotor(c); err != nil {
						return err
					}
				}
				s.printf("OK: %s braked\n", desc)
			case "run-for":
				if len(args) != 3 {
					return fmt.Errorf("usage: %s run-for [ms] [speed]", label)
				}
				ms, err := parseInt(args[1], 1, 600000, "ms")
				if err != nil {
					return err
				}
				speed, err := parseInt(args[2], -100, 100, "speed")
				if err != nil {
					return err
				}
				if err := setAll(speed); err != nil {
					return err
				}
				s.printf("OK: %s at %d for %d ms\n", desc, speed, ms)
				serr := sleepCtx(ctx, time.Duration(ms)*time.Millisecond)
				if err := setAll(0); err != nil {
					return err
				}
				if serr != nil {
					s.println("Interrupted: motor stopped")
				}
			default:
				speed, err := parseInt(args[0], -100, 100, "speed")
				if err != nil {
					return err
				}
				if err := setAll(speed); err != nil {
					return err
				}
				s.printf("OK: %s at %d\n", desc, speed)
			}
			return nil
		},
	}
}
