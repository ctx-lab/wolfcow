package protocol

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	want := Message{Type: "exec.stdout", ID: "one", Data: []byte{0, 1, 255}}
	if err := Write(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type || got.ID != want.ID || !bytes.Equal(got.Data, want.Data) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestOversizeHeaderRejectedBeforeBodyRead(t *testing.T) {
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], MaxFrame+1)
	_, err := Read(bytes.NewReader(header[:]))
	if err == nil || !strings.Contains(err.Error(), "invalid control frame size") {
		t.Fatalf("expected size rejection, got %v", err)
	}
}

func TestUnknownFieldRejected(t *testing.T) {
	body := []byte(`{"type":"file.read","path":"note","unexpected":true}`)
	var frame bytes.Buffer
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(body)))
	frame.Write(header[:])
	frame.Write(body)
	if _, err := Read(&frame); err == nil {
		t.Fatal("accepted unknown field")
	}
}
