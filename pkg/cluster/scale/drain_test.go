package scale

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDrainHappyPath(t *testing.T) {
	origInterval := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = origInterval }()

	var (
		markReadonlyHits int32
		evacuateHits     int32
		statusHits       int32
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/cluster/volumeServer.markReadonly", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&markReadonlyHits, 1)
		if got := r.URL.Query().Get("node"); got != "10.0.0.3:8080" {
			t.Errorf("unexpected node on markReadonly: %q", got)
		}
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/cluster/evacuate", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&evacuateHits, 1)
		w.WriteHeader(http.StatusOK)
	})
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

	err := Drain(srv.URL, "10.0.0.3:8080", 5*time.Second)
	if err != nil {
		t.Fatalf("Drain returned error: %v", err)
	}
	if atomic.LoadInt32(&markReadonlyHits) == 0 {
		t.Errorf("markReadonly was not called")
	}
	if atomic.LoadInt32(&evacuateHits) == 0 {
		t.Errorf("evacuate was not called")
	}
	if atomic.LoadInt32(&statusHits) < 3 {
		t.Errorf("expected /dir/status to be polled at least 3 times, got %d", statusHits)
	}
}

func TestDrainTimeout(t *testing.T) {
	origInterval := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = origInterval }()

	mux := http.NewServeMux()
	mux.HandleFunc("/cluster/volumeServer.markReadonly", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/cluster/evacuate", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/dir/status", func(w http.ResponseWriter, r *http.Request) {
		// Target never drains.
		fmt.Fprint(w, `{"Topology":{"DataCenters":[{"Racks":[{"DataNodes":[{"Url":"10.0.0.3:8080","Volumes":5}]}]}]}}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := Drain(srv.URL, "10.0.0.3:8080", 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected timeout error, got nil")
	}
}

func TestDrainEvacuateFailure(t *testing.T) {
	origInterval := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = origInterval }()

	mux := http.NewServeMux()
	mux.HandleFunc("/cluster/volumeServer.markReadonly", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/cluster/evacuate", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not implemented", http.StatusNotFound)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	err := Drain(srv.URL, "10.0.0.3:8080", 100*time.Millisecond)
	if err == nil {
		t.Fatalf("expected error when evacuate fails, got nil")
	}
}

func TestDrainNodeNotPresentCountsAsDrained(t *testing.T) {
	origInterval := pollInterval
	pollInterval = 10 * time.Millisecond
	defer func() { pollInterval = origInterval }()

	mux := http.NewServeMux()
	mux.HandleFunc("/cluster/volumeServer.markReadonly", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/cluster/evacuate", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) })
	mux.HandleFunc("/dir/status", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"Topology":{"DataCenters":[]}}`)
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	if err := Drain(srv.URL, "10.0.0.9:8080", 500*time.Millisecond); err != nil {
		t.Fatalf("expected nil error when target absent, got %v", err)
	}
}
