// Package protocol defines the bounded host/guest control stream.
package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const (
	Major         = 1
	Minor         = 0
	MaxFrame      = 1 << 20
	MaxOutput     = 16 << 20
	ControlPort   = 15000
	MaxExecMillis = 5 * 60 * 1000
)

// Message is the Stage A envelope. Data is base64 encoded by encoding/json.
// The type determines which fields are meaningful.
type Message struct {
	Type       string   `json:"type"`
	ID         string   `json:"id,omitempty"`
	Major      int      `json:"major,omitempty"`
	Minor      int      `json:"minor,omitempty"`
	Operations []string `json:"operations,omitempty"`
	Kernel     string   `json:"kernel,omitempty"`
	Argv       []string `json:"argv,omitempty"`
	Dir        string   `json:"dir,omitempty"`
	Env        []string `json:"env,omitempty"`
	DeadlineMS int64    `json:"deadline_ms,omitempty"`
	OutputMax  int64    `json:"output_max,omitempty"`
	Data       []byte   `json:"data,omitempty"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	Signal     string   `json:"signal,omitempty"`
	Status     string   `json:"status,omitempty"`
	Code       string   `json:"code,omitempty"`
	Error      string   `json:"error,omitempty"`
}

func Read(r io.Reader) (Message, error) {
	var size [4]byte
	if _, err := io.ReadFull(r, size[:]); err != nil {
		return Message{}, err
	}
	n := binary.BigEndian.Uint32(size[:])
	if n == 0 || n > MaxFrame {
		return Message{}, fmt.Errorf("invalid control frame size %d", n)
	}
	data := make([]byte, n)
	if _, err := io.ReadFull(r, data); err != nil {
		return Message{}, err
	}
	var m Message
	if err := json.Unmarshal(data, &m); err != nil {
		return Message{}, fmt.Errorf("invalid control JSON: %w", err)
	}
	if m.Type == "" {
		return Message{}, fmt.Errorf("control message missing type")
	}
	return m, nil
}

func Write(w io.Writer, m Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	if len(data) == 0 || len(data) > MaxFrame {
		return fmt.Errorf("control frame exceeds %d bytes", MaxFrame)
	}
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data)))
	if err := writeAll(w, size[:]); err != nil {
		return err
	}
	return writeAll(w, data)
}

func writeAll(w io.Writer, p []byte) error {
	for len(p) > 0 {
		n, err := w.Write(p)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		p = p[n:]
	}
	return nil
}
