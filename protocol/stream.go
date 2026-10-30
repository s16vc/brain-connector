package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

const (
	Route             = "/v1/connector"
	Version           = 1
	HandshakeTimeout  = 10 * time.Second
	KeepAliveInterval = 30 * time.Second
	MaxFieldLength    = 1024
)

type Status byte

const (
	StatusConnected  Status = 0
	StatusRefused    Status = 1
	StatusDialFailed Status = 2
)

const (
	ReplacedCloseCode = 4001
	ReplacedReason    = "replaced by another connector with the same token"
)

var ErrMalformedHeader = errors.New("malformed connector stream header")

func WriteRequest(w io.Writer, address string) error {
	return writeField(w, Version, address)
}

func ReadRequest(r io.Reader) (string, error) {
	version, address, err := readField(r)
	if err != nil {
		return "", err
	}
	if version != Version {
		return "", fmt.Errorf("%w: version %d", ErrMalformedHeader, version)
	}
	return address, nil
}

func WriteReply(w io.Writer, status Status, message string) error {
	return writeField(w, byte(status), message)
}

func ReadReply(r io.Reader) (Status, string, error) {
	status, message, err := readField(r)
	return Status(status), message, err
}

func SameAddress(a, b string) bool {
	hostA, portA, errA := net.SplitHostPort(a)
	hostB, portB, errB := net.SplitHostPort(b)
	if errA != nil || errB != nil {
		return false
	}
	numberA, errA := strconv.Atoi(portA)
	numberB, errB := strconv.Atoi(portB)
	return errA == nil && errB == nil && numberA == numberB && strings.EqualFold(strings.TrimSuffix(hostA, "."), strings.TrimSuffix(hostB, "."))
}

func writeField(w io.Writer, prefix byte, value string) error {
	if len(value) > MaxFieldLength {
		value = value[:MaxFieldLength]
	}
	frame := make([]byte, 0, 3+len(value))
	frame = append(frame, prefix)
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(value)))
	frame = append(frame, value...)
	_, err := w.Write(frame)
	return err
}

func readField(r io.Reader) (byte, string, error) {
	var header [3]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return 0, "", err
	}
	length := binary.BigEndian.Uint16(header[1:])
	if int(length) > MaxFieldLength {
		return 0, "", ErrMalformedHeader
	}
	value := make([]byte, length)
	if _, err := io.ReadFull(r, value); err != nil {
		return 0, "", err
	}
	return header[0], string(value), nil
}
