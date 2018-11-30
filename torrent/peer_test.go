package torrent

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

type readPeerMessageTest struct {
	name    string
	maxSize int
	data    []byte
	buf     []byte
	err     error
}

func errorsEqual(a, b error) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return a.Error() == b.Error()
}

func TestReadPeerMessage(t *testing.T) {
	tests := []readPeerMessageTest{
		{name: "eof", maxSize: 1024, data: []byte{}, buf: nil, err: io.EOF},
		{name: "unexpectedEof", maxSize: 1024, data: []byte{0}, buf: nil, err: io.ErrUnexpectedEOF},
		{name: "keepAlive", maxSize: 1024, data: []byte{0, 0, 0, 0}, buf: []byte{}, err: nil},
		{name: "normal", maxSize: 2, data: []byte{0, 0, 0, 2, 77, 78},
			buf: []byte{77, 78}, err: nil},
		{name: "tooLarge", maxSize: 255, data: []byte{0, 0, 1, 0}, buf: nil,
			err: errors.New("Message size too large: 256 > 255")},
	}
	for _, test := range tests {
		buffer := bytes.NewBuffer(test.data)
		buf, err := readPeerMessage(buffer, test.maxSize)
		if !errorsEqual(err, test.err) {
			t.Errorf("Test %v readPeerMessage(%v,%v) = %v,%v. expected err: %v",
				test.name, test.data, test.maxSize,
				buf, err, test.err)
		} else if !bytes.Equal(buf, test.buf) {
			t.Errorf("Test %v readPeerMessage(%v,%v) = %v,%v. expected buf: %v",
				test.name, test.data, test.maxSize,
				buf, err,
				test.buf)
		}
		if buf != nil && err == nil {
			remainingBytes := buffer.Len()
			if remainingBytes != 0 {
				t.Errorf("Test %v readPeerMessage(%v,%v) %d bytes remaining in input buffer.",
					test.name, test.data, test.maxSize,
					remainingBytes)
			}
		}
	}
}
