//go:build test_unit

package ap

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
)

type emptyMsg struct {
	proto.Message
}

func TestReadMessageLengthValidation(t *testing.T) {
	cases := []struct {
		name    string
		length  uint32
		wantErr string
	}{
		{"zero", 0, "too short"},
		{"underflow", 3, "too short"},
		{"mega", 0xFFFFFFFF, "too long"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var b []byte
			b = binary.BigEndian.AppendUint32(b, tc.length)
			var m emptyMsg
			err := readMessage(bytes.NewReader(b), -1, m)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("readMessage(length=%d) error = %v, want substring %q", tc.length, err, tc.wantErr)
			}
		})
	}
}
