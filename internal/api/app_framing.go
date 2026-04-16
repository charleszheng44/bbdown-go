package api

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
)

// packFrame wraps a protobuf payload in gRPC's length-prefixed framing.
// Layout: 1-byte compression flag (1=gzip, 0=identity), 4-byte uint32
// big-endian length, then the payload itself. When compress=true the
// payload is gzipped first and the length describes the compressed
// body.
func packFrame(payload []byte, compress bool) []byte {
	body := payload
	var flag byte
	if compress {
		var buf bytes.Buffer
		gw := gzip.NewWriter(&buf)
		_, _ = gw.Write(payload)
		_ = gw.Close()
		body = buf.Bytes()
		flag = 0x01
	}
	out := make([]byte, 5+len(body))
	out[0] = flag
	binary.BigEndian.PutUint32(out[1:5], uint32(len(body)))
	copy(out[5:], body)
	return out
}

// unpackFrame parses a gRPC-framed message and returns the inner
// (decompressed, if needed) payload. It rejects short or inconsistent
// frames with a typed error so callers can surface ErrUnknownResponse.
func unpackFrame(frame []byte) ([]byte, error) {
	if len(frame) < 5 {
		return nil, fmt.Errorf("frame truncated: %d bytes", len(frame))
	}
	flag := frame[0]
	declared := binary.BigEndian.Uint32(frame[1:5])
	body := frame[5:]
	if uint32(len(body)) < declared {
		return nil, fmt.Errorf("frame body %d bytes, header declares %d", len(body), declared)
	}
	body = body[:declared]
	switch flag {
	case 0x00:
		return body, nil
	case 0x01:
		r, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("gzip reader: %w", err)
		}
		defer r.Close()
		return io.ReadAll(r)
	default:
		return nil, fmt.Errorf("unknown frame compression flag %#x", flag)
	}
}
