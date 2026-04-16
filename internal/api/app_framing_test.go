package api

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"io"
	"testing"
)

func TestPackFrameGzipped(t *testing.T) {
	payload := []byte("hello-gzipped-payload")
	frame := packFrame(payload, true)
	if frame[0] != 0x01 {
		t.Fatalf("first byte should be 0x01 (gzip), got %#x", frame[0])
	}
	declaredLen := binary.BigEndian.Uint32(frame[1:5])
	body := frame[5:]
	if int(declaredLen) != len(body) {
		t.Errorf("declared len %d != body len %d", declaredLen, len(body))
	}
	r, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("gunzip mismatch: got %q want %q", got, payload)
	}
}

func TestPackFramePlain(t *testing.T) {
	payload := []byte("hello-plain")
	frame := packFrame(payload, false)
	if frame[0] != 0x00 {
		t.Fatalf("first byte should be 0x00 (identity), got %#x", frame[0])
	}
	if int(binary.BigEndian.Uint32(frame[1:5])) != len(payload) {
		t.Fatalf("declared len mismatch")
	}
	if !bytes.Equal(frame[5:], payload) {
		t.Fatalf("body mismatch")
	}
}

func TestUnpackFrameRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		name    string
		payload []byte
		gzip    bool
	}{
		{"gzipped", []byte("round-trip-me"), true},
		{"plain", []byte("round-trip-me"), false},
		{"empty_gzipped", []byte{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frame := packFrame(tc.payload, tc.gzip)
			got, err := unpackFrame(frame)
			if err != nil {
				t.Fatalf("unpackFrame: %v", err)
			}
			if !bytes.Equal(got, tc.payload) {
				t.Errorf("got %q want %q", got, tc.payload)
			}
		})
	}
}

func TestUnpackFrameRejectsTruncated(t *testing.T) {
	if _, err := unpackFrame([]byte{0x01, 0x00}); err == nil {
		t.Fatal("expected error on truncated frame")
	}
}
