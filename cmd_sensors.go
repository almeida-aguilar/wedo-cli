package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wedo-cli/internal/wedo"
)

type modeSpec struct {
	Name string
	Mode byte
	Unit byte // unit sent in the input format for this mode
}

// sensorSpec describes an external sensor so one menu builder serves all.
type sensorSpec struct {
	Name      string
	Type      byte
	Modes     []modeSpec // the first one must be the hub's default mode (0)
	CountMode int        // mode holding a resettable counter, -1 if none
	Format    func(mode byte, v []byte) string
}

var distanceSpec = sensorSpec{
	Name:      "distance",
	Type:      wedo.TypeMotion,
	Modes:     []modeSpec{{"distance", wedo.DistanceDetect, wedo.UnitSI}, {"count", wedo.DistanceCount, wedo.UnitSI}},
	CountMode: int(wedo.DistanceCount),
	Format: func(mode byte, v []byte) string {
		if mode == wedo.DistanceCount {
			return "Count: " + wedo.ScalarString(v)
		}
		return "Reading: " + wedo.ScalarString(v) + " (SI)"
	},
}

var tiltSpec = sensorSpec{
	Name:      "tilt",
	Type:      wedo.TypeTilt,
	Modes:     []modeSpec{{"angle", wedo.TiltAngle, wedo.UnitRaw}, {"direction", wedo.TiltDirection, wedo.UnitSI}, {"bump", wedo.TiltBump, wedo.UnitSI}},
	CountMode: int(wedo.TiltBump),
	Format: func(mode byte, v []byte) string {
		switch mode {
		case wedo.TiltAngle:
			if x, y, ok := wedo.TiltAngleValue(v); ok {
				return fmt.Sprintf("x=%d° y=%d°", x, y)
			}
		case wedo.TiltDirection:
			if n, ok := wedo.TiltDirectionName(v); ok {
				return "Orientation: " + n
			}
		case wedo.TiltBump:
			return "Count: " + wedo.ScalarString(v)
		}
		return "Value: " + wedo.HexString(v)
	},
}

func (sp sensorSpec) modeName(m byte) string {
	for _, x := range sp.Modes {
		if x.Mode == m {
			return x.Name
		}
	}
	return fmt.Sprintf("mode-%d", m)
}

// unit returns the unit that works for a mode (angle needs raw, the rest SI).
func (sp sensorSpec) unit(m byte) byte {
	for _, x := range sp.Modes {
		if x.Mode == m {
			return x.Unit
		}
	}
	return wedo.UnitSI
}

func (sp sensorSpec) modeByName(n string) (byte, bool) {
	for _, x := range sp.Modes {
		if strings.EqualFold(x.Name, n) {
			return x.Mode, true
		}
	}
	return 0, false
}

func (sp sensorSpec) modeNames() string {
	names := make([]string, len(sp.Modes))
	for i, x := range sp.Modes {
		names[i] = x.Name
	}
	return strings.Join(names, "|")
}

// read takes one sample in the given mode, switching to it if needed.
func (sp sensorSpec) read(ctx context.Context, s *Shell, conn, mode byte) error {
	if s.hub.Mode(conn) != mode {
		s.printf("(switching to %s mode)\n", sp.modeName(mode))
	}
	smp, err := s.hub.ReadSample(ctx, conn, sp.Type, mode, sp.unit(mode), 3*time.Second)
	if err != nil {
		return err
	}
	s.println(sp.Format(mode, smp.Value))
	return nil
}

func (sp sensorSpec) shortcut(pref, mode byte) RunFunc {
	return func(ctx context.Context, s *Shell, _ []string) error {
		conn, err := s.hub.FindDevice(sp.Type, pref)
		if err != nil {
			return err
		}
		return sp.read(ctx, s, conn, mode)
	}
}

// enterSensor builds the "distance [A|B]" / "tilt [A|B]" commands.
func enterSensor(sp sensorSpec, extras func(sensorSpec, byte) []Command) RunFunc {
	return func(_ context.Context, s *Shell, args []string) error {
		var pref byte
		if len(args) > 0 {
			c, ok := wedo.PortByName(args[0])
			if !ok {
				return fmt.Errorf("invalid port %q (use A or B)", args[0])
			}
			pref = c
		}
		if _, err := s.hub.FindDevice(sp.Type, pref); err != nil {
			return err
		}
		m := newSensorMenu(sp, pref)
		if extras != nil {
			m.Commands = append(m.Commands, extras(sp, pref)...)
		}
		s.push(m)
		return nil
	}
}

func tiltExtras(sp sensorSpec, pref byte) []Command {
	return []Command{
		{Name: "angle", Help: "In angle mode: x, y in degrees (-45..45)", Run: sp.shortcut(pref, wedo.TiltAngle)},
		{Name: "bump", Help: "In bump mode: bump counter", Run: sp.shortcut(pref, wedo.TiltBump)},
	}
}

func newSensorMenu(sp sensorSpec, pref byte) *Menu {
	find := func(s *Shell) (byte, error) { return s.hub.FindDevice(sp.Type, pref) }
	return &Menu{Name: sp.Name, Commands: []Command{
		{Name: "mode", Usage: "mode [" + sp.modeNames() + "]", Help: "Show or change mode",
			Run: func(_ context.Context, s *Shell, args []string) error {
				conn, err := find(s)
				if err != nil {
					return err
				}
				if len(args) == 0 {
					s.printf("Current mode: %s\n", sp.modeName(s.hub.Mode(conn)))
					return nil
				}
				m, ok := sp.modeByName(args[0])
				if !ok {
					return fmt.Errorf("unknown mode %q (use %s)", args[0], sp.modeNames())
				}
				if err := s.hub.Configure(conn, sp.Type, m, sp.unit(m)); err != nil {
					return err
				}
				s.printf("Mode %s activated\n", sp.modeName(m))
				return nil
			}},
		{Name: "read", Help: "Reading in active mode",
			Run: func(ctx context.Context, s *Shell, _ []string) error {
				conn, err := find(s)
				if err != nil {
					return err
				}
				return sp.read(ctx, s, conn, s.hub.Mode(conn))
			}},
		{Name: "reset", Help: "Reset counter, experimental (if hardware supports it)",
			Run: func(ctx context.Context, s *Shell, _ []string) error {
				conn, err := find(s)
				if err != nil {
					return err
				}
				if sp.CountMode < 0 || int(s.hub.Mode(conn)) != sp.CountMode {
					return fmt.Errorf("reset only applies in counter mode")
				}
				smp, err := s.hub.ReadSample(ctx, conn, sp.Type, byte(sp.CountMode), sp.unit(byte(sp.CountMode)), 3*time.Second)
				if err != nil {
					return err
				}
				s.printf("Mode reconfigured. %s (if not 0, hardware does not support resetting this way)\n",
					sp.Format(byte(sp.CountMode), smp.Value))
				return nil
			}},
		{Name: "listen", Help: "Listen to active mode notifications (Ctrl+C to exit)",
			Run: func(ctx context.Context, s *Shell, _ []string) error {
				conn, err := find(s)
				if err != nil {
					return err
				}
				mode := s.hub.Mode(conn)
				ch, cancel := s.hub.Subscribe()
				defer cancel()
				if err := s.hub.Configure(conn, sp.Type, mode, sp.unit(mode)); err != nil {
					return err
				}
				return s.pump(ctx, ch, func(ev wedo.Event) (string, bool) {
					if ev.Kind != wedo.EvSample || ev.Sample.Conn != conn {
						return "", false
					}
					v := ev.Sample.Value
					return fmt.Sprintf("%s [%s]", sp.Format(mode, v), wedo.HexString(v)), true
				})
			}},
	}}
}
