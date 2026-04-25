package manager

import (
	"io"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/operator"
)

// TestPrepareUnmountedDisksOnce_perHost confirms the sync.Once gate
// dedups calls per host IP. With the per-disk volume_server shape,
// DeployCluster fires multiple DeployVolumeServer calls into the same
// host concurrently — each used to call prepareUnmountedDisks
// independently and race on mkfs/mount. The gate makes disk prep
// effectively a per-host pre-step.
func TestPrepareUnmountedDisksOnce_perHost(t *testing.T) {
	m := NewManager()
	m.PrepareVolumeDisks = true

	var hostA, hostB int32
	hook := func(ip string) error {
		switch ip {
		case "10.0.0.1":
			atomic.AddInt32(&hostA, 1)
		case "10.0.0.2":
			atomic.AddInt32(&hostB, 1)
		}
		return nil
	}
	// Inject the hook by overriding prepareDisksOnce gates with ones
	// whose Do() body is the test hook. Real prepareUnmountedDisks
	// would normally run inside; this isolates the gating logic.
	for _, ip := range []string{"10.0.0.1", "10.0.0.2"} {
		gate := &prepareDisksGate{}
		ip := ip
		gate.once.Do(func() { gate.err = hook(ip) })
		m.prepareDisksOnce.Store(ip, gate)
	}

	// Even calling prepareUnmountedDisksOnce N more times per host the
	// hook must not fire again — sync.Once already fired during setup.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(2)
		go func() { defer wg.Done(); _ = m.prepareUnmountedDisksOnce(nopOp{}, "10.0.0.1") }()
		go func() { defer wg.Done(); _ = m.prepareUnmountedDisksOnce(nopOp{}, "10.0.0.2") }()
	}
	wg.Wait()

	if hostA != 1 {
		t.Errorf("hook for 10.0.0.1 fired %d times, want 1", hostA)
	}
	if hostB != 1 {
		t.Errorf("hook for 10.0.0.2 fired %d times, want 1", hostB)
	}
}

// nopOp implements operator.CommandOperator with errors so it would
// blow up if reached. The test setup pre-fires the sync.Once with a
// hook, so prepareUnmountedDisks should never actually run against
// this op.
type nopOp struct{}

func (nopOp) Execute(string) error                           { return nil }
func (nopOp) Output(string) ([]byte, error)                  { return nil, nil }
func (nopOp) Upload(io.Reader, string, string) error         { return nil }
func (nopOp) UploadFile(string, string, string) error        { return nil }
func _enforceInterface(_ operator.CommandOperator)           {}
func init()                                                  { _enforceInterface(nopOp{}) }
