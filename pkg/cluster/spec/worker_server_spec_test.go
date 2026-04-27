package spec

import (
	"bytes"
	"strings"
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
	want := "admin=10.0.0.1:23646\njobType=all\n"
	if got != want {
		t.Fatalf("WriteToBuffer with explicit admin: got %q want %q", got, want)
	}
}

func TestWorkerServerSpec_WriteToBuffer_FallbackAdmin(t *testing.T) {
	w := &WorkerServerSpec{Ip: "10.0.0.5"}
	var buf bytes.Buffer
	w.WriteToBuffer([]string{"10.0.0.1:23646", "10.0.0.2:23646"}, &buf)

	got := buf.String()
	want := "admin=10.0.0.1:23646\njobType=all\n"
	if got != want {
		t.Fatalf("WriteToBuffer fallback admin: got %q want %q", got, want)
	}
}

func TestWorkerServerSpec_WriteToBuffer_NoAdminStillEmitsJobType(t *testing.T) {
	// With no admin endpoint resolvable the worker won't actually
	// start, but WriteToBuffer's job is to render the spec — emit
	// jobType=all regardless so the on-disk options file is
	// self-describing even before deploy fills in the admin.
	w := &WorkerServerSpec{Ip: "10.0.0.5"}
	var buf bytes.Buffer
	w.WriteToBuffer(nil, &buf)

	got := buf.String()
	want := "jobType=all\n"
	if got != want {
		t.Fatalf("WriteToBuffer no-admin: got %q want %q", got, want)
	}
}

func TestWorkerServerSpec_WriteToBuffer_ExplicitJobType(t *testing.T) {
	// Operators who shard task handling across worker pools set
	// JobType per-worker; the explicit value wins over the "all"
	// default.
	w := &WorkerServerSpec{
		Ip:      "10.0.0.5",
		Admin:   "10.0.0.1:23646",
		JobType: "ec,balance",
	}
	var buf bytes.Buffer
	w.WriteToBuffer(nil, &buf)

	got := buf.String()
	if !strings.Contains(got, "jobType=ec,balance\n") {
		t.Errorf("explicit JobType not emitted: got %q", got)
	}
	if strings.Contains(got, "jobType=all") {
		t.Errorf("default jobType=all leaked when explicit value was set: got %q", got)
	}
}

func TestWorkerServerSpec_WriteToBuffer_JobTypeInConfigSuppressed(t *testing.T) {
	// jobType is a reserved key — putting it in Config would
	// produce a duplicate `-jobType` flag whose ordering is
	// shell-implementation-defined. The reserved-key filter drops
	// the Config entry; the explicit struct field's value (or "all"
	// fallback) is the only -jobType emitted.
	w := &WorkerServerSpec{
		Ip:      "10.0.0.5",
		Admin:   "10.0.0.1:23646",
		JobType: "ec",
		Config:  map[string]interface{}{"jobType": "balance"},
	}
	var buf bytes.Buffer
	w.WriteToBuffer(nil, &buf)

	got := buf.String()
	if strings.Count(got, "jobType=") != 1 {
		t.Errorf("expected exactly one jobType line, got %q", got)
	}
	if !strings.Contains(got, "jobType=ec\n") {
		t.Errorf("expected jobType=ec from struct field; got %q", got)
	}
	if strings.Contains(got, "jobType=balance") {
		t.Errorf("Config jobType should be dropped by reserved-keys filter; got %q", got)
	}
}
