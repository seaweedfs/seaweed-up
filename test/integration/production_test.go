//go:build integration
// +build integration

package integration

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestProductionSetup deploys the three-layer production topology described
// in examples/typical.yaml (storage + file access + backend operations) and
// verifies that every layer is live and cross-layer behaviors work:
//
//   - Storage:       3 masters elect a leader, 2 volume servers register
//   - File access:   writes to one filer are visible through another (they
//                    share a single external PostgreSQL metadata store),
//                    and the S3 gateway performs a round-trip
//   - Backend ops:   admin UI serves HTTP 200 and the worker systemd unit
//                    is active
//
// The CI workflow (.github/workflows/integration-production.yml) provisions
// postgres at 172.28.0.20 before the test runs; locally the test will fail
// to connect until that service is available.
func TestProductionSetup(t *testing.T) {
	env := NewTestEnvironment(t)
	env.SkipIfNotAvailable(t)

	if err := env.BuildSeaweedUp(); err != nil {
		t.Fatalf("Failed to build seaweed-up: %v", err)
	}

	if err := env.Setup(); err != nil {
		t.Fatalf("Failed to setup test environment: %v", err)
	}
	defer func() {
		if err := env.Teardown(); err != nil {
			t.Errorf("Failed to teardown test environment: %v", err)
		}
	}()

	configFile := env.GetClusterConfig("cluster-production.yaml")
	sshKey := env.GetSSHKeyPath()

	out, err := env.RunSeaweedUp(
		"cluster", "deploy", "test-production",
		"-f", configFile,
		"-u", "root",
		"--identity", sshKey,
		"--yes",
	)
	if err != nil {
		t.Logf("Deploy output: %s", out)
		t.Fatalf("cluster deploy failed: %v", err)
	}
	t.Logf("Deploy output: %s", out)

	masters := []string{"172.28.0.10", "172.28.0.11", "172.28.0.12"}
	volumes := []string{"172.28.0.11", "172.28.0.12"}
	filers := []string{"172.28.0.10", "172.28.0.11", "172.28.0.12"}
	adminIP := "172.28.0.10"
	s3IP := "172.28.0.11"
	workerHostContainer := "seaweed-up-host3"

	t.Run("StorageLayer_MastersListening", func(t *testing.T) {
		for _, ip := range masters {
			if !waitForPort(ip, 9333, 60*time.Second) {
				t.Errorf("master not listening on %s:9333", ip)
			}
		}
	})

	t.Run("StorageLayer_MasterLeaderElected", func(t *testing.T) {
		// /cluster/status on any reachable master returns Leader once the
		// Raft quorum has converged. Poll until a non-empty Leader appears
		// or we exhaust the deadline.
		deadline := time.Now().Add(90 * time.Second)
		var lastBody string
		for time.Now().Before(deadline) {
			resp, err := http.Get(fmt.Sprintf("http://%s:9333/cluster/status", masters[0]))
			if err == nil {
				body, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				lastBody = string(body)
				if strings.Contains(lastBody, "\"Leader\"") && !strings.Contains(lastBody, "\"Leader\":\"\"") {
					t.Logf("Master quorum ready: %s", truncate(lastBody, 240))
					return
				}
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("master leader not elected within 90s. last /cluster/status: %s", truncate(lastBody, 400))
	})

	t.Run("StorageLayer_VolumesRegistered", func(t *testing.T) {
		// /dir/status on the master reports the topology, including volume
		// servers that have checked in. Both configured volume IPs must
		// appear in the response.
		deadline := time.Now().Add(60 * time.Second)
		var lastBody string
		for time.Now().Before(deadline) {
			resp, err := http.Get(fmt.Sprintf("http://%s:9333/dir/status", masters[0]))
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				lastBody = string(b)
				ok := true
				for _, vip := range volumes {
					if !strings.Contains(lastBody, vip) {
						ok = false
						break
					}
				}
				if ok {
					return
				}
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("volume servers %v not registered within 60s. /dir/status: %s", volumes, truncate(lastBody, 600))
	})

	t.Run("FileAccessLayer_FilersSharePostgresNamespace", func(t *testing.T) {
		// Wait for every filer's HTTP endpoint to be ready, then write
		// through filer[0] and read back through filer[1] and filer[2].
		// Same file visible on every filer means all three share the
		// external postgres metadata store.
		for _, ip := range filers {
			if !waitForFiler(ip, 8888, 90*time.Second) {
				t.Fatalf("filer %s:8888 not ready within 90s", ip)
			}
		}

		writeURL := fmt.Sprintf("http://%s:8888/production-test.txt", filers[0])
		content := "three-filer-one-namespace"
		if err := filerMultipartPost(writeURL, "production-test.txt", content); err != nil {
			t.Fatalf("POST to filer %s: %v", filers[0], err)
		}

		for _, ip := range filers[1:] {
			readURL := fmt.Sprintf("http://%s:8888/production-test.txt", ip)
			got, err := httpGetString(readURL)
			if err != nil {
				t.Errorf("GET via filer %s: %v", ip, err)
				continue
			}
			if got != content {
				t.Errorf("filer %s returned %q, want %q", ip, got, content)
			}
		}
	})

	t.Run("FileAccessLayer_S3RoundTrip", func(t *testing.T) {
		if !waitForPort(s3IP, 8333, 60*time.Second) {
			t.Fatalf("S3 gateway not listening on %s:8333", s3IP)
		}
		if _, err := exec.LookPath("aws"); err != nil {
			t.Skip("aws CLI not installed; skipping S3 round-trip")
		}

		endpoint := fmt.Sprintf("http://%s:8333", s3IP)
		awsEnv := append(os.Environ(),
			"AWS_ACCESS_KEY_ID=any",
			"AWS_SECRET_ACCESS_KEY=any",
			"AWS_DEFAULT_REGION=us-east-1",
			"AWS_EC2_METADATA_DISABLED=true",
		)
		runAws := func(args ...string) (string, error) {
			full := append([]string{"--endpoint-url", endpoint, "--no-verify-ssl"}, args...)
			cmd := exec.Command("aws", full...)
			cmd.Env = awsEnv
			o, err := cmd.CombinedOutput()
			return string(o), err
		}

		if o, err := runAws("s3", "mb", "s3://prod"); err != nil {
			t.Fatalf("mb failed: %v\n%s", err, o)
		}
		src := filepath.Join(t.TempDir(), "prod.txt")
		payload := "hello from production-setup test"
		if err := os.WriteFile(src, []byte(payload), 0644); err != nil {
			t.Fatalf("write src: %v", err)
		}
		if o, err := runAws("s3", "cp", src, "s3://prod/prod.txt"); err != nil {
			t.Fatalf("upload failed: %v\n%s", err, o)
		}
		dst := filepath.Join(t.TempDir(), "prod-downloaded.txt")
		if o, err := runAws("s3", "cp", "s3://prod/prod.txt", dst); err != nil {
			t.Fatalf("download failed: %v\n%s", err, o)
		}
		got, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read downloaded: %v", err)
		}
		if string(got) != payload {
			t.Fatalf("S3 round-trip mismatch: got %q", string(got))
		}
	})

	t.Run("BackendOps_AdminUI", func(t *testing.T) {
		url := fmt.Sprintf("http://%s:23646/", adminIP)
		deadline := time.Now().Add(60 * time.Second)
		var status int
		var lastBody string
		for time.Now().Before(deadline) {
			resp, err := http.Get(url)
			if err == nil {
				b, _ := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				status = resp.StatusCode
				lastBody = string(b)
				if status == http.StatusOK && strings.Contains(lastBody, "SeaweedFS") {
					return
				}
			}
			time.Sleep(2 * time.Second)
		}
		t.Fatalf("admin UI at %s never returned 200 with SeaweedFS body (last status=%d, body=%q)", url, status, truncate(lastBody, 200))
	})

	t.Run("BackendOps_WorkerActive", func(t *testing.T) {
		if !waitForSystemdActive(workerHostContainer, "seaweed_worker0", 60*time.Second) {
			t.Fatalf("seaweed_worker0 not active on %s within 60s", workerHostContainer)
		}
	})

	t.Run("Lifecycle_ComponentScopedStopStartWorker", func(t *testing.T) {
		// Exercise the new --component=worker lifecycle path: stop should
		// bring only the worker unit down, start should bring it back,
		// and neither should touch the admin or filer units.
		if o, err := env.RunSeaweedUp(
			"cluster", "stop",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--component", "worker",
			"--yes",
		); err != nil {
			t.Fatalf("cluster stop --component=worker failed: %v\n%s", err, o)
		}
		if !waitForSystemdState(workerHostContainer, "seaweed_worker0", "inactive", 30*time.Second) &&
			!waitForSystemdState(workerHostContainer, "seaweed_worker0", "failed", 30*time.Second) {
			t.Fatalf("worker did not enter inactive/failed state after targeted stop")
		}
		// Admin must still be active.
		if !waitForSystemdActive("seaweed-up-host1", "seaweed_admin0", 10*time.Second) {
			t.Errorf("admin should stay active during component-scoped worker stop")
		}

		if o, err := env.RunSeaweedUp(
			"cluster", "start",
			"-f", configFile,
			"-u", "root",
			"--identity", sshKey,
			"--component", "worker",
			"--yes",
		); err != nil {
			t.Fatalf("cluster start --component=worker failed: %v\n%s", err, o)
		}
		if !waitForSystemdActive(workerHostContainer, "seaweed_worker0", 30*time.Second) {
			t.Fatalf("worker did not return to active after targeted start")
		}
	})
}

// waitForFiler polls the filer root until it responds with any status < 500.
// The filer needs a moment after deploy to connect to postgres and bring up
// its HTTP listener.
func waitForFiler(ip string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://%s:%d/", ip, port)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode < 500 {
				return true
			}
		}
		time.Sleep(2 * time.Second)
	}
	return false
}

// filerMultipartPost uploads content to a filer at url under the given
// filename using multipart/form-data, which is what the SeaweedFS filer
// upload endpoint expects.
func filerMultipartPost(url, filename, content string) error {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		return fmt.Errorf("create form file: %w", err)
	}
	if _, err := fw.Write([]byte(content)); err != nil {
		return fmt.Errorf("write form file: %w", err)
	}
	if err := mw.Close(); err != nil {
		return fmt.Errorf("close multipart: %w", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("filer returned status %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// httpGetString fetches url and returns the response body as a string.
func httpGetString(url string) (string, error) {
	resp, err := http.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// waitForSystemdActive polls `systemctl is-active <unit>` inside the named
// container until the unit reports active or the timeout expires.
func waitForSystemdActive(container, unit string, timeout time.Duration) bool {
	return waitForSystemdState(container, unit, "active", timeout)
}

// waitForSystemdState polls for a specific systemctl is-active output.
func waitForSystemdState(container, unit, wantState string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("docker", "exec", container, "systemctl", "is-active", unit).CombinedOutput()
		if strings.TrimSpace(string(out)) == wantState {
			return true
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return false
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
