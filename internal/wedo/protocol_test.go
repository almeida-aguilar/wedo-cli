package wedo

import (
	"bytes"
	"testing"
)

func eq(t *testing.T, name string, got, want []byte) {
	t.Helper()
	if !bytes.Equal(got, want) {
		t.Errorf("%s: got % x, want % x", name, got, want)
	}
}

// Expected bytes come from the BLED112 captures with the framing removed.
func TestEncoders(t *testing.T) {
	eq(t, "motor", MotorPower(1, -87), []byte{0x01, 0x01, 0x01, 0xa9})
	eq(t, "motor clamp", MotorPower(1, 500), []byte{0x01, 0x01, 0x01, 100})
	eq(t, "piezo", PiezoTone(262, 500), []byte{0x05, 0x02, 0x04, 0x06, 0x01, 0xf4, 0x01})
	eq(t, "led index", LEDIndex(4), []byte{0x06, 0x04, 0x01, 0x04})
	eq(t, "led rgb", LEDRGB(0, 0xff, 0x40), []byte{0x06, 0x04, 0x03, 0x00, 0xff, 0x40})
	eq(t, "tilt format", InputFormat(1, TypeTilt, 1, 1, UnitSI, true),
		[]byte{0x01, 0x02, 0x01, 0x22, 0x01, 0x01, 0x00, 0x00, 0x00, 0x02, 0x01})
	eq(t, "rgb discrete", InputFormat(ConnRGB, TypeRGB, LEDDiscrete, 1, UnitRaw, true),
		[]byte{0x01, 0x02, 0x06, 0x17, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x01})
	eq(t, "start", InputStart(2), []byte{0x00, 0x01, 0x02})
}

func TestParsePortEvent(t *testing.T) {
	ev, err := ParsePortEvent([]byte{0x01, 0x01, 0x00, 0x22, 0, 0, 0, 0x10, 0, 0, 0, 0x10})
	if err != nil || ev.Conn != 1 || !ev.Attached || ev.Type != TypeTilt {
		t.Fatalf("attach: %+v %v", ev, err)
	}
	ev, err = ParsePortEvent([]byte{0x02, 0x00})
	if err != nil || ev.Conn != 2 || ev.Attached {
		t.Fatalf("detach: %+v %v", ev, err)
	}
	if _, err := ParsePortEvent([]byte{0x01}); err == nil {
		t.Fatal("expected error for short event")
	}
}

func TestParseSample(t *testing.T) {
	s, err := ParseSample([]byte{0x03, 0x02, 0x00, 0x00, 0xa0, 0x40})
	if err != nil || s.Conn != 2 {
		t.Fatalf("%+v %v", s, err)
	}
	if got := ScalarString(s.Value); got != "5" {
		t.Errorf("scalar = %q, want 5", got)
	}
}

func TestDecoders(t *testing.T) {
	x, y, ok := TiltAngleValue([]byte{0xfb, 0x0a})
	if !ok || x != -5 || y != 10 {
		t.Errorf("angle = %d,%d,%v", x, y, ok)
	}
	if n, _ := TiltDirectionName([]byte{9}); n != "forward" {
		t.Errorf("direction = %q", n)
	}
	if got := ScalarString([]byte{1, 2, 3}); got != "01 02 03" {
		t.Errorf("fallback = %q", got)
	}
}

func TestParseHex(t *testing.T) {
	for _, in := range []string{"04 01 01 a9", "0x04,0x01,0x01,0xa9", "040101a9"} {
		b, err := ParseHex(in)
		if err != nil {
			t.Fatalf("%q: %v", in, err)
		}
		eq(t, in, b, []byte{0x04, 0x01, 0x01, 0xa9})
	}
	if _, err := ParseHex("abc"); err == nil {
		t.Error("expected error for odd length")
	}
}
