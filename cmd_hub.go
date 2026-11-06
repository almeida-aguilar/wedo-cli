package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"wedo-cli/internal/wedo"
)

func hubMenu() *Menu {
	return &Menu{Name: "hub", Commands: []Command{
		{Name: "battery", Help: "Battery level (0-100%)", Run: cmdBattery},
		{Name: "voltage", Help: "Battery voltage", Run: func(ctx context.Context, s *Shell, _ []string) error {
			return readInternal(ctx, s, wedo.ConnVoltage, wedo.TypeVoltage, "Voltage")
		}},
		{Name: "current", Help: "Current consumed by hub (extra)", Run: func(ctx context.Context, s *Shell, _ []string) error {
			return readInternal(ctx, s, wedo.ConnCurrent, wedo.TypeCurrent, "Current")
		}},
		{Name: "status", Help: "Summary: connection, name, battery, ports, alerts", Run: cmdStatus},
		{Name: "port", Help: "Connected device on each port (A/B)", Run: cmdPort},
		{Name: "info", Help: "Name, manufacturer, firmware, and software (extra)", Run: cmdInfo},
		{Name: "name", Usage: "name [new]", Help: "Show or change hub name, max 16 bytes (extra)", Run: cmdName},
		{Name: "listen", Usage: "listen alerts|port|all", Help: "Listen to alerts, attach/detach, or all (Ctrl+C to exit)", Run: cmdListen},
		{Name: "vcc", Usage: "vcc [0|1]", Help: "Read or write Vcc port control, experimental (extra)", Run: cmdVcc},
		{Name: "led", Help: "Enter LED menu", Run: enter(ledMenu)},
		{Name: "button", Help: "Enter button menu", Run: enter(buttonMenu)},
		{Name: "buzzer", Help: "Enter buzzer menu", Run: enter(buzzerMenu)},
		{Name: "motor", Help: "Enter motor menu", Run: enter(motorMenu)},
		{Name: "distance", Usage: "distance [A|B]", Help: "Enter distance sensor menu", Run: enterSensor(distanceSpec, nil)},
		{Name: "tilt", Usage: "tilt [A|B]", Help: "Enter tilt sensor menu", Run: enterSensor(tiltSpec, tiltExtras)},
		{Name: "raw", Help: "Raw GATT tools: gatt, read, write, notify (extra)", Run: enter(rawMenu)},
		{Name: "disconnect", Help: "Close BLE connection (hub stays on)", Run: cmdDisconnect},
		{Name: "shutdown", Help: "Power off the hub (must be turned on manually)", Run: cmdShutdown},
	}}
}

func portDescription(s *Shell, conn byte) string {
	if ev, ok := s.hub.Ports()[conn]; ok {
		return wedo.TypeName(ev.Type)
	}
	return "(empty)"
}

func cmdBattery(_ context.Context, s *Shell, _ []string) error {
	lvl, err := s.hub.Battery()
	if err != nil {
		return err
	}
	s.printf("Battery: %d%%\n", lvl)
	return nil
}

func readInternal(ctx context.Context, s *Shell, conn, typ byte, label string) error {
	smp, err := s.hub.ReadSample(ctx, conn, typ, 0, wedo.UnitSI, 3*time.Second)
	if err != nil {
		return err
	}
	s.printf("%s: %s (SI; raw %s)\n", label, wedo.ScalarString(smp.Value), wedo.HexString(smp.Value))
	return nil
}

func alertState(s *Shell, c wedo.CharRef) string {
	b, err := s.hub.Read(c)
	if err != nil || len(b) == 0 {
		return "?"
	}
	if b[0] != 0 {
		return "ON"
	}
	return "off"
}

func cmdStatus(_ context.Context, s *Shell, _ []string) error {
	state := "connected"
	if !s.hub.Alive() {
		state = "disconnected"
	}
	s.printf("Connection:  %s\n", state)
	if n, err := s.hub.HubName(); err == nil {
		s.printf("Name:        %s\n", n)
	}
	if lvl, err := s.hub.Battery(); err == nil {
		s.printf("Battery:     %d%%\n", lvl)
	} else {
		s.printf("Battery:     ? (%v)\n", err)
	}
	s.printf("Port A:      %s\n", portDescription(s, wedo.ConnPortA))
	s.printf("Port B:      %s\n", portDescription(s, wedo.ConnPortB))
	ports := s.hub.Ports()
	var internals []string
	for c := wedo.ConnCurrent; c <= wedo.ConnRGB; c++ {
		if ev, ok := ports[c]; ok {
			internals = append(internals, wedo.TypeName(ev.Type))
		}
	}
	if len(internals) > 0 {
		s.printf("Internal:    %s\n", strings.Join(internals, ", "))
	}
	s.printf("Alerts:      low-voltage=%s high-current=%s low-signal=%s\n",
		alertState(s, wedo.CharLowVoltage), alertState(s, wedo.CharHighCurrent), alertState(s, wedo.CharLowSignal))
	return nil
}

func cmdPort(_ context.Context, s *Shell, _ []string) error {
	s.printf("Port A: %s\n", portDescription(s, wedo.ConnPortA))
	s.printf("Port B: %s\n", portDescription(s, wedo.ConnPortB))
	return nil
}

func cmdInfo(_ context.Context, s *Shell, _ []string) error {
	items := []struct {
		label string
		ref   wedo.CharRef
	}{
		{"Name", wedo.CharDeviceName},
		{"Manufacturer", wedo.CharManufacturer},
		{"Firmware", wedo.CharFirmware},
		{"Software", wedo.CharSoftware},
	}
	for _, it := range items {
		b, err := s.hub.Read(it.ref)
		if err != nil {
			s.printf("%-13s ? (%v)\n", it.label+":", err)
			continue
		}
		s.printf("%-13s %s\n", it.label+":", strings.TrimRight(string(b), "\x00"))
	}
	if b, err := s.hub.Read(wedo.CharBatteryType); err == nil && len(b) > 0 {
		kind := "unknown"
		if b[0] == 0 {
			kind = "AA, per capture"
		}
		s.printf("%-13s %02x (%s)\n", "Battery type:", b[0], kind)
	}
	return nil
}

func cmdName(_ context.Context, s *Shell, args []string) error {
	if len(args) == 0 {
		n, err := s.hub.HubName()
		if err != nil {
			return err
		}
		s.printf("Name: %s\n", n)
		return nil
	}
	name := strings.Join(args, " ")
	if err := s.hub.Rename(name); err != nil {
		return err
	}
	s.printf("Name changed to %q (may need reconnect to see it)\n", name)
	return nil
}

func cmdVcc(_ context.Context, s *Shell, args []string) error {
	if len(args) == 0 {
		b, err := s.hub.Read(wedo.CharVcc)
		if err != nil {
			return err
		}
		s.printf("Vcc port control: %s\n", wedo.HexString(b))
		return nil
	}
	v, err := parseInt(args[0], 0, 1, "value")
	if err != nil {
		return err
	}
	if err := s.hub.Write(wedo.CharVcc, []byte{byte(v)}, true); err != nil {
		return err
	}
	s.printf("Vcc port control = %d\n", v)
	return nil
}

func describeEvent(ev wedo.Event) string {
	switch ev.Kind {
	case wedo.EvButton:
		if ev.Pressed {
			return "button: PRESSED"
		}
		return "button: RELEASED"
	case wedo.EvPort:
		if ev.Port.Attached {
			return fmt.Sprintf("port %s: connected (%s)", wedo.PortLabel(ev.Port.Conn), wedo.TypeName(ev.Port.Type))
		}
		return fmt.Sprintf("port %s: disconnected", wedo.PortLabel(ev.Port.Conn))
	case wedo.EvSample:
		v := ev.Sample.Value
		return fmt.Sprintf("value %s: %s [%s]", wedo.PortLabel(ev.Sample.Conn), wedo.ScalarString(v), wedo.HexString(v))
	case wedo.EvAlert:
		if ev.Active {
			return ev.Alert + ": ACTIVE"
		}
		return ev.Alert + ": inactive"
	case wedo.EvFormat:
		return "format: " + wedo.HexString(ev.Raw)
	}
	return ""
}

func onlyKind(k wedo.EventKind) func(wedo.Event) (string, bool) {
	return func(ev wedo.Event) (string, bool) {
		if ev.Kind != k {
			return "", false
		}
		return describeEvent(ev), true
	}
}

func cmdListen(ctx context.Context, s *Shell, args []string) error {
	usage := fmt.Errorf("usage: listen alerts|port|all")
	if len(args) == 0 {
		return usage
	}
	alerts := []wedo.CharRef{wedo.CharLowVoltage, wedo.CharHighCurrent, wedo.CharLowSignal}
	ch, cancel := s.hub.Subscribe()
	defer cancel()
	switch strings.ToLower(args[0]) {
	case "alerts":
		enableAll(s, alerts...)
		return s.pump(ctx, ch, onlyKind(wedo.EvAlert))
	case "port":
		enableAll(s, wedo.CharPort)
		return s.pump(ctx, ch, onlyKind(wedo.EvPort))
	case "all":
		enableAll(s, append(alerts, wedo.CharButton, wedo.CharPort)...)
		return s.pump(ctx, ch, func(ev wedo.Event) (string, bool) {
			l := describeEvent(ev)
			return l, l != ""
		})
	}
	return usage
}

func cmdDisconnect(_ context.Context, s *Shell, _ []string) error {
	_ = s.hub.Close()
	s.println("Disconnected. Hub remains powered on.")
	return errQuit
}

func cmdShutdown(_ context.Context, s *Shell, _ []string) error {
	if err := s.hub.TurnOff(); err != nil {
		return err
	}
	_ = s.hub.Detach()
	s.println("Hub shut down. Must be turned on manually.")
	return errQuit
}
