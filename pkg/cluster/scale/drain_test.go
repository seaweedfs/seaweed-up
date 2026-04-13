package scale

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitForDrainHappyPath(t *testing.T) {
	origInterval := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = origInterval }()

	var statusHits int32

	mux := http.NewServeMux()
	mux.HandleFunc("/dir/status", func(w http.ResponseWriter, r *http.Request) {
		hit := atomic.AddInt32(&statusHits, 1)
		// First two polls show 3 then 1 volume, then drained.
		volumes := 0
		switch hit {
		case 1:
			volumes = 3
		case 2:
			volumes = 1
		default:
			volumes = 0
		}
		fmt.Fprintf(w, `{"Topology":{"DataCenters":[{"Racks":[{"DataNodes":[{"Url":"10.0.0.3:8080","PublicUrl":"10.0.0.3:8080","Volumes":%d}]}]}]}}`, volumes)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := WaitForDrain(srv.URL, "10.0.0.3:8080", 5*time.Second); err != nil {
		t.Fatalf("WaitForDrain returned error: %v", err)
	}
	if atomic.LoadInt32(&statusHits) < 3 {
		t.Errorf("expected /dir/status to be polled at least 3 times, got %d", statusHits)
	}
}

func TestWaitForDrainTimeout(t *testing.T) {
	origInterval := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = origInterval }()

	mux := http.NewServeMux()
	mux.HandleFunc("/dir/status", func(w http.ResponseWriter, r *http.Request) {
		// Target never drains.
		fmt.Fprint(w, `{"Topology":{"DataCenters":[{"Racks":[{"DataNodes":[{"Url":"10.0.0.3:8080","Volumes":5}]}]}]}}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := WaitForDrain(srv.URL, "10.0.0.3:8080", 100*time.Millisecond); err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}

func TestWaitForDrainNodeNotPresentCountsAsDrained(t *testing.T) {
	origInterval := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = origInterval }()

	mux := http.NewServeMux()
	mux.HandleFunc("/dir/status", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Topology":{"DataCenters":[]}}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := WaitForDrain(srv.URL, "10.0.0.9:8080", 500*time.Millisecond); err != nil {
		t.Fatalf("expected nil error when target absent, got %v", err)
	}
}
