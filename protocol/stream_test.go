package protocol_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/s16vc/brain-connector/protocol"
)

func TestRequestHeaderLayout(t *testing.T) {
	var wire bytes.Buffer
	if err := protocol.WriteRequest(&wire, "db.internal:5432"); err != nil {
		t.Fatal(err)
	}
	want := append([]byte{1, 0, 16}, "db.internal:5432"...)
	if !bytes.Equal(wire.Bytes(), want) {
		t.Fatalf("request bytes = %v, want %v", wire.Bytes(), want)
	}
	address, err := protocol.ReadRequest(&wire)
	if err != nil || address != "db.internal:5432" {
		t.Fatalf("read request = %q, %v", address, err)
	}
}

func TestReplyHeaderLayout(t *testing.T) {
	cases := []struct {
		status  protocol.Status
		code    byte
		message string
	}{
		{protocol.StatusConnected, 0, ""},
		{protocol.StatusRefused, 1, "this connector only reaches db:5432, not other:5432"},
		{protocol.StatusDialFailed, 2, "the connector could not reach db:5432"},
	}
	for _, tc := range cases {
		var wire bytes.Buffer
		if err := protocol.WriteReply(&wire, tc.status, tc.message); err != nil {
			t.Fatal(err)
		}
		raw := wire.Bytes()
		if raw[0] != tc.code || binary.BigEndian.Uint16(raw[1:3]) != uint16(len(tc.message)) || string(raw[3:]) != tc.message {
			t.Fatalf("reply bytes = %v", raw)
		}
		status, message, err := protocol.ReadReply(&wire)
		if err != nil || status != tc.status || message != tc.message {
			t.Fatalf("read reply = %d %q %v", status, message, err)
		}
	}
}

func TestRequestRefusesAnotherVersion(t *testing.T) {
	wire := bytes.NewReader(append([]byte{2, 0, 6}, "db:543"...))
	if _, err := protocol.ReadRequest(wire); !errors.Is(err, protocol.ErrMalformedHeader) {
		t.Fatalf("error = %v", err)
	}
}

func TestFieldsOverTheLimit(t *testing.T) {
	oversize := binary.BigEndian.AppendUint16([]byte{protocol.Version}, protocol.MaxFieldLength+1)
	if _, err := protocol.ReadRequest(bytes.NewReader(oversize)); !errors.Is(err, protocol.ErrMalformedHeader) {
		t.Fatalf("oversize request error = %v", err)
	}
	var wire bytes.Buffer
	if err := protocol.WriteReply(&wire, protocol.StatusDialFailed, strings.Repeat("x", 3*protocol.MaxFieldLength)); err != nil {
		t.Fatal(err)
	}
	_, message, err := protocol.ReadReply(&wire)
	if err != nil || len(message) != protocol.MaxFieldLength {
		t.Fatalf("long message read back as %d bytes, %v", len(message), err)
	}
}

func TestTruncatedHeaders(t *testing.T) {
	if _, err := protocol.ReadRequest(bytes.NewReader([]byte{1, 0})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short header error = %v", err)
	}
	if _, _, err := protocol.ReadReply(bytes.NewReader([]byte{0, 0, 5, 'a'})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("short message error = %v", err)
	}
}

func TestSameAddress(t *testing.T) {
	cases := map[[2]string]bool{
		{"db.internal:5432", "DB.INTERNAL:5432"}:  true,
		{"db.internal:5432", "db.internal.:5432"}: true,
		{"db.internal:5432", "db.internal:05432"}: true,
		{"[::1]:5432", "[::1]:5432"}:              true,
		{"db.internal:5432", "db.internal:5433"}:  false,
		{"db.internal:5432", "other:5432"}:        false,
		{"db.internal:pg", "db.internal:pg"}:      false,
		{"db.internal", "db.internal"}:            false,
	}
	for pair, want := range cases {
		if got := protocol.SameAddress(pair[0], pair[1]); got != want {
			t.Errorf("SameAddress(%q, %q) = %v", pair[0], pair[1], got)
		}
	}
}

func TestWireConstantsMatchTheContract(t *testing.T) {
	if protocol.Route != "/v1/connector" || protocol.Version != 1 || protocol.MaxFieldLength != 1024 {
		t.Fatalf("route %q, version %d, field limit %d", protocol.Route, protocol.Version, protocol.MaxFieldLength)
	}
	if protocol.ReplacedCloseCode != 4001 || protocol.ReplacedReason != "replaced by another connector with the same token" {
		t.Fatalf("close = %d %q", protocol.ReplacedCloseCode, protocol.ReplacedReason)
	}
	if protocol.TokenPrefix != "bdc_" || protocol.TokenVersion != 1 {
		t.Fatalf("token prefix %q, version %d", protocol.TokenPrefix, protocol.TokenVersion)
	}
}
