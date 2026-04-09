package spec

import (
	"bytes"
	"testing"
)

func TestWorkerServerSpec_WriteToBuffer_ExplicitAdmin(t *testing.T) {
	w := &WorkerServerSpec{
		Ip:    "10.0.0.5",
		Admin: "10.0.0.1:23646",
	}
	var buf bytes.Buffer
	w.WriteToBuffer(nil, &buf)

	got := buf.String()
	want := "admin=10.0.0.1:23646\n"
	if got != want {
		t.Fatalf("WriteToBuffer with explicit admin: got %q want %q", got, want)
	}
}

func TestWorkerServerSpec_WriteToBuffer_FallbackAdmin(t *testing.T) {
	w := &WorkerServerSpec{Ip: "10.0.0.5"}
	var buf bytes.Buffer
	w.WriteToBuffer([]string{"10.0.0.1:23646", "10.0.0.2:23646"}, &buf)

	got := buf.String()
	want := "admin=10.0.0.1:23646\n"
	if got != want {
		t.Fatalf("WriteToBuffer fallback admin: got %q want %q", got, want)
	}
}

func TestWorkerServerSpec_WriteToBuffer_NoAdmin(t *testing.T) {
	w := &WorkerServerSpec{Ip: "10.0.0.5"}
	var buf bytes.Buffer
	w.WriteToBuffer(nil, &buf)

	if buf.Len() != 0 {
		t.Fatalf("expected empty buffer when no admin supplied, got %q", buf.String())
	}
}
