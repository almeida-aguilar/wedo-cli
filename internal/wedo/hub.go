package wedo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"wedo-cli/internal/ble"
)

// ErrDisconnected is returned once the hub link is gone.
var ErrDisconnected = errors.New("hub disconnected")

// EventKind tells what an Event carries.
type EventKind int

const (
	EvButton EventKind = iota + 1
	EvPort
	EvSample
	EvAlert
	EvDisconnect
	EvFormat
)

// Event is anything the hub notifies us about.
type Event struct {
	Kind    EventKind
	Time    time.Time
	Raw     []byte
	Pressed bool      // EvButton
	Port    PortEvent // EvPort
	Sample  Sample    // EvSample
	Alert   string    // EvAlert: LOW_VOLTAGE, HIGH_CURRENT or LOW_SIGNAL
	Active  bool      // EvAlert
}

type bus struct {
	mu   sync.Mutex
	next int
	subs map[int]chan Event
}

func (b *bus) subscribe() (<-chan Event, func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs == nil {
		b.subs = map[int]chan Event{}
	}
	id := b.next
	b.next++
	ch := make(chan Event, 128)
	b.subs[id] = ch
	return ch, func() {
		b.mu.Lock()
		delete(b.subs, id)
		b.mu.Unlock()
	}
}

func (b *bus) publish(e Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs {
		select {
		case ch <- e:
		default: // slow consumer: drop
		}
	}
}

// Hub is a connected WeDo 2.0 Smart Hub.
type Hub struct {
	p   ble.Peripheral
	log *slog.Logger
	bus bus

	mu        sync.Mutex
	ports     map[byte]PortEvent
	modes     map[byte]byte
	notifying map[string]bool
	ledMode   int
	gone      bool

	closeOnce sync.Once
}

func newHub(p ble.Peripheral, log *slog.Logger) *Hub {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Hub{
		p:         p,
		log:       log,
		ports:     map[byte]PortEvent{},
		modes:     map[byte]byte{},
		notifying: map[string]bool{},
		ledMode:   -1,
	}
}

// Open connects to the hub with the given scan ID, starts listening for
// port and disconnect notifications and waits for the initial port table.
func Open(be ble.Backend, id string, log *slog.Logger) (*Hub, error) {
	p, err := be.Connect(id)
	if err != nil {
		return nil, fmt.Errorf("connecting: %w", err)
	}
	h := newHub(p, log)
	if !h.hasService(ServiceHub) {
		h.log.Warn("device does not expose the WeDo 2.0 service", "services", p.Services())
	}
	if err := h.EnableNotify(CharDisconnect); err != nil {
		h.log.Debug("disconnect notifications unavailable", "err", err)
	}
	if err := h.settlePorts(); err != nil {
		h.log.Warn("port notifications unavailable", "err", err)
	}
	return h, nil
}

func (h *Hub) hasService(uuid string) bool {
	for _, s := range h.p.Services() {
		if s == uuid {
			return true
		}
	}
	return false
}

// settlePorts enables port notifications; the hub then replays one attach
// event per device. We wait until the burst is over.
func (h *Hub) settlePorts() error {
	ch, cancel := h.bus.subscribe()
	defer cancel()
	if err := h.EnableNotify(CharPort); err != nil {
		return err
	}
	wait := 2 * time.Second
	for {
		select {
		case ev := <-ch:
			if ev.Kind == EvPort {
				wait = 400 * time.Millisecond
			}
		case <-time.After(wait):
			return nil
		}
	}
}

// Subscribe returns a channel with every hub event until cancel is called.
// Subscribe before enabling notifications to see their initial events.
func (h *Hub) Subscribe() (<-chan Event, func()) { return h.bus.subscribe() }

func (h *Hub) isGone() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.gone
}

func (h *Hub) markGone() {
	h.mu.Lock()
	h.gone = true
	h.mu.Unlock()
}

// Alive reports whether the BLE link is still up.
func (h *Hub) Alive() bool { return !h.isGone() && h.p.Connected() }

// Services lists the GATT services found on the hub.
func (h *Hub) Services() []string { return h.p.Services() }

func (h *Hub) char(c CharRef) (ble.Characteristic, error) {
	ch, ok := h.p.Characteristic(c.Service, c.Char)
	if !ok {
		return nil, fmt.Errorf("hub does not expose %s", c.Name)
	}
	return ch, nil
}

// Read reads a characteristic.
func (h *Hub) Read(c CharRef) ([]byte, error) {
	if h.isGone() {
		return nil, ErrDisconnected
	}
	ch, err := h.char(c)
	if err != nil {
		return nil, err
	}
	return ch.Read()
}

// Write writes a characteristic.
func (h *Hub) Write(c CharRef, data []byte, withResponse bool) error {
	if h.isGone() {
		return ErrDisconnected
	}
	ch, err := h.char(c)
	if err != nil {
		return err
	}
	h.log.Debug("write", "char", c.Name, "data", HexString(data))
	return ch.Write(data, withResponse)
}

// EnableNotify turns on notifications for a known characteristic (once).
func (h *Hub) EnableNotify(c CharRef) error {
	h.mu.Lock()
	already := h.notifying[c.Char]
	h.mu.Unlock()
	if already {
		return nil
	}
	ch, err := h.char(c)
	if err != nil {
		return err
	}
	if err := ch.Notify(h.handler(c)); err != nil {
		return fmt.Errorf("enabling notifications for %s: %w", c.Name, err)
	}
	h.mu.Lock()
	h.notifying[c.Char] = true
	h.mu.Unlock()
	return nil
}

func (h *Hub) handler(c CharRef) func([]byte) {
	return func(b []byte) {
		raw := append([]byte(nil), b...)
		now := time.Now()
		h.log.Debug("notify", "char", c.Name, "data", HexString(raw))
		switch c.Char {
		case CharButton.Char:
			if len(raw) == 0 {
				return
			}
			h.bus.publish(Event{Kind: EvButton, Time: now, Raw: raw, Pressed: raw[0] != 0})
		case CharPort.Char:
			ev, err := ParsePortEvent(raw)
			if err != nil {
				h.log.Debug("bad port event", "err", err, "raw", HexString(raw))
				return
			}
			h.mu.Lock()
			if ev.Attached {
				h.ports[ev.Conn] = ev
			} else {
				delete(h.ports, ev.Conn)
			}
			delete(h.modes, ev.Conn) // a new/removed device starts at its default mode
			h.mu.Unlock()
			h.bus.publish(Event{Kind: EvPort, Time: now, Raw: raw, Port: ev})
		case CharSensor.Char:
			s, err := ParseSample(raw)
			if err != nil {
				h.log.Debug("bad sample", "err", err, "raw", HexString(raw))
				return
			}
			h.bus.publish(Event{Kind: EvSample, Time: now, Raw: raw, Sample: s})
		case CharLowVoltage.Char:
			h.alert("LOW_VOLTAGE", raw, now)
		case CharHighCurrent.Char:
			h.alert("HIGH_CURRENT", raw, now)
		case CharLowSignal.Char:
			h.alert("LOW_SIGNAL", raw, now)
		case CharFormat.Char:
			h.bus.publish(Event{Kind: EvFormat, Time: now, Raw: raw})
		case CharDisconnect.Char:
			h.markGone()
			h.bus.publish(Event{Kind: EvDisconnect, Time: now, Raw: raw})
		}
	}
}

func (h *Hub) alert(name string, raw []byte, now time.Time) {
	h.bus.publish(Event{Kind: EvAlert, Time: now, Raw: raw, Alert: name, Active: len(raw) > 0 && raw[0] != 0})
}

// Ports returns a copy of the attached-device table, keyed by connect ID.
func (h *Hub) Ports() map[byte]PortEvent {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[byte]PortEvent, len(h.ports))
	for k, v := range h.ports {
		out[k] = v
	}
	return out
}

// FindDevice returns the external port holding a device of the given type.
// pref is ConnPortA/ConnPortB to force a port, or 0 to auto-select.
func (h *Hub) FindDevice(typ, pref byte) (byte, error) {
	ports := h.Ports()
	match := func(c byte) bool {
		ev, ok := ports[c]
		return ok && ev.Type == typ
	}
	if pref != 0 {
		if !match(pref) {
			return 0, fmt.Errorf("no %s device on port %s", TypeName(typ), PortLabel(pref))
		}
		return pref, nil
	}
	var found []byte
	for _, c := range []byte{ConnPortA, ConnPortB} {
		if match(c) {
			found = append(found, c)
		}
	}
	switch len(found) {
	case 0:
		return 0, fmt.Errorf("no %s device connected", TypeName(typ))
	case 1:
		return found[0], nil
	}
	return 0, fmt.Errorf("multiple %s devices found: specify port (A or B)", TypeName(typ))
}

// ---- Inputs (sensors) ----

// SetInputMode sends the input format for a device (no notification setup).
func (h *Hub) SetInputMode(conn, typ, mode, unit byte) error {
	if err := h.Write(CharInputCmd, InputFormat(conn, typ, mode, 1, unit, true), true); err != nil {
		return err
	}
	h.mu.Lock()
	h.modes[conn] = mode
	h.mu.Unlock()
	return nil
}

// Configure selects a sensor mode, enables value notifications and sends
// the start signal.
func (h *Hub) Configure(conn, typ, mode, unit byte) error {
	if err := h.EnableNotify(CharSensor); err != nil {
		return err
	}
	if err := h.SetInputMode(conn, typ, mode, unit); err != nil {
		return err
	}
	if err := h.Write(CharInputCmd, InputStart(conn), true); err != nil {
		h.log.Debug("start signal rejected", "err", err)
	}
	return nil
}

// Mode returns the last mode configured for a connect ID (0 if unknown).
func (h *Hub) Mode(conn byte) byte {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.modes[conn]
}

// ReadSample configures the sensor and waits for its next value.
func (h *Hub) ReadSample(ctx context.Context, conn, typ, mode, unit byte, timeout time.Duration) (Sample, error) {
	ch, cancel := h.bus.subscribe()
	defer cancel()
	if err := h.Configure(conn, typ, mode, unit); err != nil {
		return Sample{}, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	for {
		select {
		case ev := <-ch:
			switch {
			case ev.Kind == EvSample && ev.Sample.Conn == conn:
				return ev.Sample, nil
			case ev.Kind == EvDisconnect:
				return Sample{}, ErrDisconnected
			}
		case <-t.C:
			return Sample{}, fmt.Errorf("hub did not send any value within %s (did the value change?)", timeout)
		case <-ctx.Done():
			return Sample{}, ctx.Err()
		}
	}
}

// ---- Outputs ----

func (h *Hub) SetMotor(conn byte, speed int) error {
	return h.Write(CharOutputCmd, MotorPower(conn, speed), true)
}

func (h *Hub) BrakeMotor(conn byte) error {
	return h.Write(CharOutputCmd, MotorBrake(conn), true)
}

func (h *Hub) Tone(freq, ms uint16) error {
	return h.Write(CharOutputCmd, PiezoTone(freq, ms), true)
}

func (h *Hub) StopTone() error { return h.Write(CharOutputCmd, PiezoStop(), true) }

func (h *Hub) ensureLEDMode(mode byte) error {
	h.mu.Lock()
	cur := h.ledMode
	h.mu.Unlock()
	if cur == int(mode) {
		return nil
	}
	if err := h.SetInputMode(ConnRGB, TypeRGB, mode, UnitRaw); err != nil {
		return err
	}
	h.mu.Lock()
	h.ledMode = int(mode)
	h.mu.Unlock()
	return nil
}

// LEDIndex shows one of the predefined colours.
func (h *Hub) LEDIndex(i byte) error {
	if err := h.ensureLEDMode(LEDDiscrete); err != nil {
		return err
	}
	return h.Write(CharOutputCmd, LEDIndex(i), true)
}

// LEDRGB shows an absolute colour.
func (h *Hub) LEDRGB(r, g, b byte) error {
	if err := h.ensureLEDMode(LEDAbsolute); err != nil {
		return err
	}
	return h.Write(CharOutputCmd, LEDRGB(r, g, b), true)
}

// ---- Hub properties ----

func (h *Hub) HubName() (string, error) {
	b, err := h.Read(CharDeviceName)
	return strings.TrimRight(string(b), "\x00"), err
}

// Rename writes a new hub name (1-16 bytes).
func (h *Hub) Rename(name string) error {
	if len(name) == 0 || len(name) > 16 {
		return fmt.Errorf("name must be between 1 and 16 bytes")
	}
	return h.Write(CharHubName, []byte(name), true)
}

// Battery returns the battery level in percent.
func (h *Hub) Battery() (int, error) {
	b, err := h.Read(CharBattery)
	if err != nil {
		return 0, err
	}
	if len(b) == 0 {
		return 0, fmt.Errorf("empty battery read")
	}
	return int(b[0]), nil
}

// TurnOff powers the hub down (it must be switched on by hand afterwards).
func (h *Hub) TurnOff() error { return h.Write(CharTurnOff, []byte{0x01}, true) }

// SafeStop silences motors and the piezo.
func (h *Hub) SafeStop() {
	for conn, ev := range h.Ports() {
		if ev.Type == TypeMotor {
			_ = h.SetMotor(conn, 0)
		}
	}
	_ = h.StopTone()
}

func (h *Hub) close(stop bool) error {
	var err error
	h.closeOnce.Do(func() {
		if stop && h.Alive() {
			h.SafeStop()
		}
		h.markGone()
		err = h.p.Disconnect()
	})
	return err
}

// Close stops motors/sound and drops the BLE link (the hub stays on).
func (h *Hub) Close() error { return h.close(true) }

// Detach drops the link without touching the outputs (after TurnOff).
func (h *Hub) Detach() error { return h.close(false) }

// ---- Raw GATT access ----

// Resolve finds a characteristic by 16-bit short UUID (WeDo base first,
// then the Bluetooth base) or by full UUID, searching every service.
func (h *Hub) Resolve(s string) (ble.Characteristic, string, error) {
	s = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "0x")
	var cands []string
	if len(s) <= 4 && !strings.Contains(s, "-") {
		n, err := strconv.ParseUint(s, 16, 16)
		if err != nil {
			return nil, "", fmt.Errorf("invalid UUID %q", s)
		}
		cands = []string{hubUUID(uint16(n)), stdUUID(uint16(n))}
	} else {
		u, err := ble.NormalizeUUID(s)
		if err != nil {
			return nil, "", err
		}
		cands = []string{u}
	}
	for _, svc := range h.p.Services() {
		for _, c := range cands {
			if ch, ok := h.p.Characteristic(svc, c); ok {
				return ch, c, nil
			}
		}
	}
	return nil, "", fmt.Errorf("characteristic %s not found", s)
}

func (h *Hub) ReadRaw(uuid string) ([]byte, error) {
	ch, _, err := h.Resolve(uuid)
	if err != nil {
		return nil, err
	}
	return ch.Read()
}

func (h *Hub) WriteRaw(uuid string, data []byte, withResponse bool) error {
	ch, _, err := h.Resolve(uuid)
	if err != nil {
		return err
	}
	return ch.Write(data, withResponse)
}

// RawNotify streams raw notifications to fn until ctx is done, then puts
// the characteristic back the way it was (our handler, or off).
func (h *Hub) RawNotify(ctx context.Context, uuid string, fn func([]byte)) error {
	ch, full, err := h.Resolve(uuid)
	if err != nil {
		return err
	}
	if err := ch.Notify(func(b []byte) { fn(append([]byte(nil), b...)) }); err != nil {
		return err
	}
	<-ctx.Done()
	h.mu.Lock()
	known := h.notifying[full]
	h.mu.Unlock()
	if known {
		for _, ref := range AllChars {
			if ref.Char == full {
				_ = ch.Notify(h.handler(ref))
				return nil
			}
		}
	}
	_ = ch.Notify(nil)
	return nil
}

// ProbeResult describes one known characteristic found (or not) on the hub.
type ProbeResult struct {
	Ref     CharRef
	Present bool
	Value   []byte
	Err     error
}

// Probe checks every known characteristic and reads the readable ones.
func (h *Hub) Probe() []ProbeResult {
	out := make([]ProbeResult, 0, len(AllChars))
	for _, ref := range AllChars {
		r := ProbeResult{Ref: ref}
		ch, ok := h.p.Characteristic(ref.Service, ref.Char)
		r.Present = ok
		if ok && ref.Readable {
			r.Value, r.Err = ch.Read()
		}
		out = append(out, r)
	}
	return out
}
