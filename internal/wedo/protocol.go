// Package wedo implements the LEGO WeDo 2.0 Smart Hub GATT protocol on top
// of the ble abstraction. Nothing in this package prints; the CLI does that.
//
// Payloads are the native BLE ones: the BLED112 framing seen in the
// captures (00 08 04 06 00 3d 00 ...) is stripped, only the bytes written
// to the characteristic remain.
package wedo

import (
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"strconv"
	"strings"
)

const (
	hubSuffix = "-1212-efde-1523-785feabcd123"
	stdSuffix = "-0000-1000-8000-00805f9b34fb"
)

func hubUUID(short uint16) string { return fmt.Sprintf("0000%04x%s", short, hubSuffix) }
func stdUUID(short uint16) string { return fmt.Sprintf("0000%04x%s", short, stdSuffix) }

// CharRef identifies one GATT characteristic.
type CharRef struct {
	Service  string
	Char     string
	Name     string
	Readable bool // reading is known to work (from the captured GATT dump)
}

var (
	ServiceGAP        = stdUUID(0x1800)
	ServiceDeviceInfo = stdUUID(0x180a)
	ServiceBattery    = stdUUID(0x180f)
	ServiceHub        = hubUUID(0x1523)
	ServiceIO         = hubUUID(0x4f0e)
)

var (
	// Standard services.
	CharDeviceName   = CharRef{ServiceGAP, stdUUID(0x2a00), "Device Name (GAP)", true}
	CharFirmware     = CharRef{ServiceDeviceInfo, stdUUID(0x2a26), "Firmware Revision", true}
	CharSoftware     = CharRef{ServiceDeviceInfo, stdUUID(0x2a28), "Software Revision", true}
	CharManufacturer = CharRef{ServiceDeviceInfo, stdUUID(0x2a29), "Manufacturer Name", true}
	CharBattery      = CharRef{ServiceBattery, stdUUID(0x2a19), "Battery Level", true}

	// Hub service 0x1523.
	CharHubName     = CharRef{ServiceHub, hubUUID(0x1524), "Hub Name (write)", false}
	CharButton      = CharRef{ServiceHub, hubUUID(0x1526), "Button", true}
	CharPort        = CharRef{ServiceHub, hubUUID(0x1527), "Port Type", true}
	CharLowVoltage  = CharRef{ServiceHub, hubUUID(0x1528), "Low Voltage alert", true}
	CharHighCurrent = CharRef{ServiceHub, hubUUID(0x1529), "High Current alert", true}
	CharLowSignal   = CharRef{ServiceHub, hubUUID(0x152a), "Low Signal alert", true}
	CharTurnOff     = CharRef{ServiceHub, hubUUID(0x152b), "Turn off device", false}
	CharVcc         = CharRef{ServiceHub, hubUUID(0x152c), "Vcc port control", true}
	CharBatteryType = CharRef{ServiceHub, hubUUID(0x152d), "Battery type", true}
	CharDisconnect  = CharRef{ServiceHub, hubUUID(0x152e), "Disconnect", false}

	// I/O service 0x4f0e.
	CharSensor    = CharRef{ServiceIO, hubUUID(0x1560), "Sensor Value", false}
	CharFormat    = CharRef{ServiceIO, hubUUID(0x1561), "Value Format", true}
	CharInputCmd  = CharRef{ServiceIO, hubUUID(0x1563), "Input Command", false}
	CharOutputCmd = CharRef{ServiceIO, hubUUID(0x1565), "Output Command", false}
)

// AllChars lists every characteristic this package knows about.
var AllChars = []CharRef{
	CharDeviceName, CharFirmware, CharSoftware, CharManufacturer, CharBattery,
	CharHubName, CharButton, CharPort, CharLowVoltage, CharHighCurrent,
	CharLowSignal, CharTurnOff, CharVcc, CharBatteryType, CharDisconnect,
	CharSensor, CharFormat, CharInputCmd, CharOutputCmd,
}

// Connect IDs: the index the hub uses to address each I/O.
const (
	ConnPortA   byte = 0x01
	ConnPortB   byte = 0x02
	ConnCurrent byte = 0x03
	ConnVoltage byte = 0x04
	ConnPiezo   byte = 0x05
	ConnRGB     byte = 0x06
)

// Device (type) IDs reported in port-type notifications.
const (
	TypeMotor   byte = 0x01
	TypeVoltage byte = 0x14
	TypeCurrent byte = 0x15
	TypePiezo   byte = 0x16
	TypeRGB     byte = 0x17
	TypeTilt    byte = 0x22
	TypeMotion  byte = 0x23 // the "distance" sensor
	TypeGeneric byte = 0x24
)

// Units for the input format command.
const (
	UnitRaw     byte = 0
	UnitPercent byte = 1
	UnitSI      byte = 2
)

// RGB LED modes.
const (
	LEDAbsolute byte = 0 // r, g, b
	LEDDiscrete byte = 1 // predefined colour index
)

// Tilt sensor modes (bump/crash is mode 2 per LEGO docs; unverified here).
const (
	TiltAngle     byte = 0
	TiltDirection byte = 1
	TiltBump      byte = 2
)

// Distance (motion) sensor modes.
const (
	DistanceDetect byte = 0
	DistanceCount  byte = 1
)

// TypeName returns a short human name for a device type.
func TypeName(t byte) string {
	switch t {
	case TypeMotor:
		return "motor"
	case TypeVoltage:
		return "voltage"
	case TypeCurrent:
		return "current"
	case TypePiezo:
		return "piezo"
	case TypeRGB:
		return "rgb-led"
	case TypeTilt:
		return "tilt"
	case TypeMotion:
		return "distance"
	case TypeGeneric:
		return "generic"
	}
	return fmt.Sprintf("unknown(0x%02x)", t)
}

// PortLabel names a connect ID: A and B for the external ports.
func PortLabel(conn byte) string {
	switch conn {
	case ConnPortA:
		return "A"
	case ConnPortB:
		return "B"
	}
	return fmt.Sprintf("internal-%d", conn)
}

// PortByName parses "A" or "B" (any case) into a connect ID.
func PortByName(s string) (byte, bool) {
	switch strings.ToUpper(s) {
	case "A":
		return ConnPortA, true
	case "B":
		return ConnPortB, true
	}
	return 0, false
}

// ---- Output command (0x1565): [connectId, commandId, len, data...] ----

// MotorPower builds a motor power write. speed is clamped to -100..100;
// the byte is a two's complement int8 (0 = float/coast).
func MotorPower(conn byte, speed int) []byte {
	if speed > 100 {
		speed = 100
	}
	if speed < -100 {
		speed = -100
	}
	return []byte{conn, 0x01, 0x01, byte(int8(speed))}
}

// MotorBrake builds the active-brake write (power value 0x7f).
func MotorBrake(conn byte) []byte { return []byte{conn, 0x01, 0x01, 0x7f} }

// PiezoTone plays freq Hz for ms milliseconds.
func PiezoTone(freq, ms uint16) []byte {
	b := []byte{ConnPiezo, 0x02, 0x04, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(b[3:5], freq)
	binary.LittleEndian.PutUint16(b[5:7], ms)
	return b
}

// PiezoStop silences the piezo.
func PiezoStop() []byte { return []byte{ConnPiezo, 0x03, 0x00} }

// LEDIndex sets a predefined colour (the LED must be in discrete mode).
func LEDIndex(i byte) []byte { return []byte{ConnRGB, 0x04, 0x01, i} }

// LEDRGB sets an absolute colour (the LED must be in absolute mode).
func LEDRGB(r, g, b byte) []byte { return []byte{ConnRGB, 0x04, 0x03, r, g, b} }

// ---- Input command (0x1563) ----

// InputFormat configures a device mode: [01 02 conn type mode delta(4) unit notify].
func InputFormat(conn, typ, mode byte, delta uint32, unit byte, notify bool) []byte {
	b := []byte{0x01, 0x02, conn, typ, mode, 0, 0, 0, 0, unit, 0}
	binary.LittleEndian.PutUint32(b[5:9], delta)
	if notify {
		b[10] = 1
	}
	return b
}

// InputStart is the "start sensor working" signal seen in the captures.
func InputStart(conn byte) []byte { return []byte{0x00, 0x01, conn} }

// ---- Notifications ----

// PortEvent is a notification on the Port Type characteristic.
type PortEvent struct {
	Conn     byte
	Attached bool
	HubIndex byte
	Type     byte
}

// ParsePortEvent decodes either the 2-byte detach form [conn 00] or the
// 12-byte attach form [conn 01 hubIndex type 8 bytes of settings].
func ParsePortEvent(b []byte) (PortEvent, error) {
	if len(b) < 2 {
		return PortEvent{}, fmt.Errorf("port event too short (%d bytes)", len(b))
	}
	ev := PortEvent{Conn: b[0], Attached: b[1] == 1}
	if ev.Attached {
		if len(b) < 4 {
			return PortEvent{}, fmt.Errorf("attach event too short (%d bytes)", len(b))
		}
		ev.HubIndex = b[2]
		ev.Type = b[3]
	}
	return ev, nil
}

// Sample is a Sensor Value notification: [?, conn, value...].
type Sample struct {
	Conn  byte
	Value []byte
	Raw   []byte
}

// ParseSample decodes a Sensor Value notification.
func ParseSample(b []byte) (Sample, error) {
	if len(b) < 2 {
		return Sample{}, fmt.Errorf("sensor value too short (%d bytes)", len(b))
	}
	return Sample{Conn: b[1], Value: b[2:], Raw: b}, nil
}

// ---- Value decoding ----

// ScalarValue interprets a sensor value by length: 1 byte = uint8,
// 2 = uint16 LE, 4 = float32 LE (what the SI captures show).
func ScalarValue(v []byte) (float64, bool) {
	switch len(v) {
	case 1:
		return float64(v[0]), true
	case 2:
		return float64(binary.LittleEndian.Uint16(v)), true
	case 4:
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(v))), true
	}
	return 0, false
}

// ScalarString renders ScalarValue, falling back to hex.
func ScalarString(v []byte) string {
	f, ok := ScalarValue(v)
	if !ok {
		return HexString(v)
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e9 {
		return strconv.FormatInt(int64(f), 10)
	}
	return strconv.FormatFloat(f, 'f', 2, 64)
}

// TiltAngleValue decodes angle mode: x and y as signed degrees (-45..45).
func TiltAngleValue(v []byte) (x, y int, ok bool) {
	if len(v) == 8 { // SI unit: two float32 (x, y)
		x := math.Float32frombits(binary.LittleEndian.Uint32(v[0:4]))
		y := math.Float32frombits(binary.LittleEndian.Uint32(v[4:8]))
		return int(math.Round(float64(x))), int(math.Round(float64(y))), true
	}
	if len(v) < 2 {
		return 0, 0, false
	}
	return int(int8(v[0])), int(int8(v[1])), true
}

// TiltDirectionName maps the direction-mode code to a name. The mapping is
// the commonly documented one (0 neutral, 3 back, 5 right, 7 left,
// 9 forward) and has NOT been verified against a real hub yet.
func TiltDirectionName(v []byte) (string, bool) {
	f, ok := ScalarValue(v)
	if !ok {
		return "", false
	}
	switch int(f) {
	case 0:
		return "neutral", true
	case 3:
		return "back", true
	case 5:
		return "right", true
	case 7:
		return "left", true
	case 9:
		return "forward", true
	}
	return fmt.Sprintf("unknown(%v)", f), true
}

// HexString formats bytes as "00 ff 10".
func HexString(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = fmt.Sprintf("%02x", x)
	}
	return strings.Join(parts, " ")
}

// ParseHex accepts "04 01 01 a9", "0x04,0x01" or "040101a9".
func ParseHex(s string) ([]byte, error) {
	s = strings.NewReplacer("0x", "", ",", "", " ", "", ":", "", "-", "").Replace(strings.ToLower(s))
	if s == "" {
		return nil, fmt.Errorf("no bytes provided")
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("invalid hex: %w", err)
	}
	return b, nil
}
