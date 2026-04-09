package preflight

import (
	"context"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fatih/color"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
	"github.com/seaweedfs/seaweed-up/pkg/operator"
	"golang.org/x/sync/errgroup"
)

// DefaultSSHPort is the fallback SSH port when the spec does not set one.
const DefaultSSHPort = 22

// DefaultCheckConcurrency is the default number of hosts checked in parallel.
const DefaultCheckConcurrency = 8

// envoyAdminDefaultPort is the envoy admin listener port used when the spec
// does not expose a configurable value. Derived from pkg/cluster/manager/envoy.yaml.tpl.
const envoyAdminDefaultPort = 9901

// Result is the outcome of a single preflight check on a single host.
type Result struct {
	Name   string `json:"name"`
	Host   string `json:"host"`
	OK     bool   `json:"ok"`
	Warn   bool   `json:"warn,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// Runner abstracts remote command execution on a host. The SSH factory in
// cmd/cluster_check.go produces one of these per host; tests inject a fake.
type Runner interface {
	Output(command string) ([]byte, error)
}

// SSHFactory opens a Runner to a given host. It is responsible for closing
// whatever it opens before returning.
type SSHFactory func(ctx context.Context, host string, port int, fn func(Runner) error) error

// HostPlan describes the component roles and expected ports scheduled on a
// single host according to the cluster specification.
type HostPlan struct {
	Host    string
	SSHPort int
	DataDir string
	Arch    string
	OS      string
	// Ports that MUST be free before deploy.
	Ports []int
	// Component labels scheduled here (master/volume/filer/envoy).
	Components []string
}

// Check is a single preflight check executed against a host plan.
type Check interface {
	Name() string
	Run(ctx context.Context, plan HostPlan, r Runner) Result
}

// BuildHostPlans converts a cluster spec into a per-host plan describing all
// ports and components scheduled on that host. The defaultSSHPort is used as
// the fallback when the spec entry does not set a per-host SSH port.
func BuildHostPlans(s *spec.Specification, defaultSSHPort int) []HostPlan {
	if defaultSSHPort <= 0 {
		defaultSSHPort = DefaultSSHPort
	}
	dataDir := s.GlobalOptions.DataDir
	if dataDir == "" {
		dataDir = "/opt/seaweed"
	}
	osName := s.GlobalOptions.OS
	if osName == "" {
		osName = "linux"
	}

	byHost := map[string]*HostPlan{}
	get := func(ip string, sshPort int) *HostPlan {
		if p, ok := byHost[ip]; ok {
			return p
		}
		p := &HostPlan{
			Host:    ip,
			SSHPort: sshPort,
			DataDir: dataDir,
			OS:      osName,
		}
		byHost[ip] = p
		return p
	}

	addPort := func(p *HostPlan, port int) {
		if port <= 0 {
			return
		}
		for _, e := range p.Ports {
			if e == port {
				return
			}
		}
		p.Ports = append(p.Ports, port)
	}
	addComp := func(p *HostPlan, c string) {
		for _, e := range p.Components {
			if e == c {
				return
			}
		}
		p.Components = append(p.Components, c)
	}

	for _, m := range s.MasterServers {
		sshPort := m.PortSsh
		if sshPort == 0 {
			sshPort = defaultSSHPort
		}
		p := get(m.Ip, sshPort)
		port := m.Port
		if port == 0 {
			port = 9333
		}
		grpc := m.PortGrpc
		if grpc == 0 {
			grpc = 19333
		}
		addPort(p, port)
		addPort(p, grpc)
		addComp(p, "master")
		if m.Arch != "" {
			p.Arch = m.Arch
		}
		if m.OS != "" {
			p.OS = m.OS
		}
	}
	for _, v := range s.VolumeServers {
		sshPort := v.PortSsh
		if sshPort == 0 {
			sshPort = defaultSSHPort
		}
		p := get(v.Ip, sshPort)
		port := v.Port
		if port == 0 {
			port = 8080
		}
		grpc := v.PortGrpc
		if grpc == 0 {
			grpc = 18080
		}
		addPort(p, port)
		addPort(p, grpc)
		addComp(p, "volume")
		if v.Arch != "" {
			p.Arch = v.Arch
		}
		if v.OS != "" {
			p.OS = v.OS
		}
	}
	for _, f := range s.FilerServers {
		sshPort := f.PortSsh
		if sshPort == 0 {
			sshPort = defaultSSHPort
		}
		p := get(f.Ip, sshPort)
		port := f.Port
		if port == 0 {
			port = 8888
		}
		grpc := f.PortGrpc
		if grpc == 0 {
			grpc = 18888
		}
		addPort(p, port)
		addPort(p, grpc)
		s3p := f.S3Port
		if s3p == 0 {
			s3p = 8333
		}
		addPort(p, s3p)
		addComp(p, "filer")
		if f.Arch != "" {
			p.Arch = f.Arch
		}
		if f.OS != "" {
			p.OS = f.OS
		}
	}
	for _, e := range s.EnvoyServers {
		sshPort := e.PortSsh
		if sshPort == 0 {
			sshPort = defaultSSHPort
		}
		p := get(e.Ip, sshPort)
		// Forwarded listener ports: derive from spec where set, fall back to
		// the documented defaults used by pkg/cluster/spec/envoy_server_spec.go.
		filerPort := e.FilerPort
		if filerPort == 0 {
			filerPort = 8888
		}
		filerGrpcPort := e.FilerGrpcPort
		if filerGrpcPort == 0 {
			filerGrpcPort = 18888
		}
		s3Port := e.S3Port
		if s3Port == 0 {
			s3Port = 8333
		}
		webdavPort := e.WebdavPort
		if webdavPort == 0 {
			webdavPort = 7333
		}
		addPort(p, filerPort)
		addPort(p, filerGrpcPort)
		addPort(p, s3Port)
		addPort(p, webdavPort)
		// Envoy admin listener - not configurable in the current spec, so use
		// the default documented in manager/envoy.yaml.tpl.
		addPort(p, envoyAdminDefaultPort)
		addComp(p, "envoy")
	}

	out := make([]HostPlan, 0, len(byHost))
	for _, p := range byHost {
		sort.Ints(p.Ports)
		sort.Strings(p.Components)
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Host < out[j].Host })
	return out
}

// ParseListeningPorts parses the output of `ss -tln` and returns the set of
// local TCP ports currently bound. Both IPv4 and IPv6 addresses are handled.
func ParseListeningPorts(out string) map[int]bool {
	ports := map[int]bool{}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Skip header lines.
		lower := strings.ToLower(line)
		if strings.HasPrefix(lower, "state") || strings.HasPrefix(lower, "netid") {
			continue
		}
		fields := strings.Fields(line)
		// ss -tln output columns: State Recv-Q Send-Q Local Address:Port Peer Address:Port ...
		// Find the first field that contains ':' and looks like host:port.
		var localAddr string
		for i := 3; i < len(fields) && i < 5; i++ {
			if strings.Contains(fields[i], ":") {
				localAddr = fields[i]
				break
			}
		}
		if localAddr == "" {
			continue
		}
		idx := strings.LastIndex(localAddr, ":")
		if idx < 0 {
			continue
		}
		portStr := localAddr[idx+1:]
		if p, err := strconv.Atoi(portStr); err == nil {
			ports[p] = true
		}
	}
	return ports
}

// ParseFreeKB parses the output of `df -P <path>` and returns the available
// space in kilobytes for the relevant filesystem.
func ParseFreeKB(out string) (int64, error) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, fmt.Errorf("unexpected df output: %q", out)
	}
	fields := strings.Fields(lines[len(lines)-1])
	// POSIX df -P columns: Filesystem 1024-blocks Used Available Capacity Mounted-on
	if len(fields) < 4 {
		return 0, fmt.Errorf("unexpected df fields: %q", lines[len(lines)-1])
	}
	avail, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cannot parse available blocks %q: %w", fields[3], err)
	}
	return avail, nil
}

// --- concrete checks ---

// sshCheck verifies we successfully opened a session to the host. Because the
// SSH factory is invoked before any per-host check runs, reaching this check
// at all means the connection itself is up; we still emit an explicit OK
// result so reports make connectivity status visible.
type sshCheck struct{}

func (sshCheck) Name() string { return "ssh" }
func (sshCheck) Run(ctx context.Context, plan HostPlan, r Runner) Result {
	// A trivial command confirms the session can execute remote commands.
	if _, err := r.Output("true"); err != nil {
		return Result{Name: "ssh", Host: plan.Host, OK: false, Detail: "ssh session unusable: " + err.Error()}
	}
	return Result{Name: "ssh", Host: plan.Host, OK: true, Detail: "ssh session ok"}
}

// sudoCheck verifies passwordless sudo works for the SSH user. This is only
// executed when the underlying ssh session is already usable.
type sudoCheck struct{}

func (sudoCheck) Name() string { return "sudo" }
func (sudoCheck) Run(ctx context.Context, plan HostPlan, r Runner) Result {
	out, err := r.Output("sudo -n true 2>&1 || true")
	if err != nil {
		return Result{Name: "sudo", Host: plan.Host, OK: false, Detail: err.Error()}
	}
	txt := strings.TrimSpace(string(out))
	if txt == "" {
		return Result{Name: "sudo", Host: plan.Host, OK: true, Detail: "sudo -n ok"}
	}
	return Result{Name: "sudo", Host: plan.Host, OK: false, Detail: "sudo not passwordless: " + txt}
}

type diskSpaceCheck struct{}

func (diskSpaceCheck) Name() string { return "disk-space" }
func (diskSpaceCheck) Run(ctx context.Context, plan HostPlan, r Runner) Result {
	cmd := fmt.Sprintf("mkdir -p %s 2>/dev/null; df -P %s", shellQuote(plan.DataDir), shellQuote(plan.DataDir))
	out, err := r.Output(cmd)
	if err != nil {
		return Result{Name: "disk-space", Host: plan.Host, OK: false, Detail: err.Error()}
	}
	availKB, err := ParseFreeKB(string(out))
	if err != nil {
		return Result{Name: "disk-space", Host: plan.Host, OK: false, Detail: err.Error()}
	}
	const minKB int64 = 1024 * 1024 // 1 GiB
	if availKB < minKB {
		return Result{
			Name: "disk-space", Host: plan.Host, OK: false,
			Detail: fmt.Sprintf("%s: %d KB free, need >= %d KB", plan.DataDir, availKB, minKB),
		}
	}
	return Result{
		Name: "disk-space", Host: plan.Host, OK: true,
		Detail: fmt.Sprintf("%s: %d KB free", plan.DataDir, availKB),
	}
}

type portsFreeCheck struct{}

func (portsFreeCheck) Name() string { return "ports-free" }
func (portsFreeCheck) Run(ctx context.Context, plan HostPlan, r Runner) Result {
	if len(plan.Ports) == 0 {
		return Result{Name: "ports-free", Host: plan.Host, OK: true, Detail: "no ports scheduled"}
	}
	out, err := r.Output("ss -tln 2>/dev/null || netstat -tln 2>/dev/null")
	if err != nil {
		return Result{Name: "ports-free", Host: plan.Host, OK: false, Detail: err.Error()}
	}
	busy := ParseListeningPorts(string(out))
	var busyPorts []int
	for _, p := range plan.Ports {
		if busy[p] {
			busyPorts = append(busyPorts, p)
		}
	}
	if len(busyPorts) == 0 {
		return Result{
			Name: "ports-free", Host: plan.Host, OK: true,
			Detail: fmt.Sprintf("%d ports free", len(plan.Ports)),
		}
	}

	// Try to identify the process(es) holding each busy port. We prefer
	// `ss -tlnp` (requires root for the comm/PID columns but still emits
	// the rows for non-matching processes), and fall back to lsof. The
	// output may be empty on hosts where neither tool exposes the owner,
	// in which case we just report the port numbers.
	owners := map[int]portOwner{}
	if pout, perr := r.Output("ss -tlnp 2>/dev/null"); perr == nil && len(pout) > 0 {
		for k, v := range ParseListeningPortOwners(string(pout)) {
			owners[k] = v
		}
	}
	for _, p := range busyPorts {
		if _, ok := owners[p]; ok {
			continue
		}
		// Fall back to lsof for this specific port.
		cmd := fmt.Sprintf("lsof -nP -iTCP:%d -sTCP:LISTEN 2>/dev/null | awk 'NR>1 {print $1\" \"$2; exit}'", p)
		if lo, lerr := r.Output(cmd); lerr == nil {
			line := strings.TrimSpace(string(lo))
			if line != "" {
				parts := strings.Fields(line)
				if len(parts) >= 2 {
					owners[p] = portOwner{Process: parts[0], PID: parts[1]}
				}
			}
		}
	}

	// Split conflicts into "weed" holders (stale processes that the deploy
	// will replace - WARN) and genuine conflicts with other processes (FAIL).
	var weedDetails, otherDetails []string
	for _, p := range busyPorts {
		o, known := owners[p]
		switch {
		case known && isWeedProcess(o.Process):
			weedDetails = append(weedDetails,
				fmt.Sprintf("%d (weed pid %s)", p, o.PID))
		case known:
			otherDetails = append(otherDetails,
				fmt.Sprintf("%d (%s pid %s)", p, o.Process, o.PID))
		default:
			otherDetails = append(otherDetails, strconv.Itoa(p))
		}
	}

	if len(otherDetails) == 0 {
		// Only stale weed processes. Deploy will replace them, so WARN.
		return Result{
			Name: "ports-free", Host: plan.Host, OK: true, Warn: true,
			Detail: "stale weed holding ports: " + strings.Join(weedDetails, ", "),
		}
	}
	detail := "ports in use: " + strings.Join(otherDetails, ", ")
	if len(weedDetails) > 0 {
		detail += "; stale weed: " + strings.Join(weedDetails, ", ")
	}
	return Result{
		Name: "ports-free", Host: plan.Host, OK: false,
		Detail: detail,
	}
}

// portOwner identifies the process listening on a TCP port.
type portOwner struct {
	Process string
	PID     string
}

func isWeedProcess(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "weed"
}

// ParseListeningPortOwners parses the output of `ss -tlnp` and returns a
// map from port number to the first process observed holding it. The
// owner column from ss looks like:
//
//	users:(("weed",pid=1234,fd=7))
//
// We extract the command name and pid.
func ParseListeningPortOwners(out string) map[int]portOwner {
	owners := map[int]portOwner{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(strings.ToLower(line), "state") {
			continue
		}
		fields := strings.Fields(line)
		// ss -tlnp columns: State Recv-Q Send-Q Local-Address:Port Peer-Address:Port [users:(...)]
		if len(fields) < 4 {
			continue
		}
		local := fields[3]
		idx := strings.LastIndex(local, ":")
		if idx < 0 {
			continue
		}
		port, err := strconv.Atoi(local[idx+1:])
		if err != nil {
			continue
		}
		if _, exists := owners[port]; exists {
			continue
		}
		// Find the users:((...)) blob anywhere in the line.
		usersIdx := strings.Index(line, "users:((")
		if usersIdx < 0 {
			owners[port] = portOwner{}
			continue
		}
		blob := line[usersIdx+len("users:(("):]
		end := strings.Index(blob, "))")
		if end >= 0 {
			blob = blob[:end]
		}
		// blob is like: "weed",pid=1234,fd=7
		parts := strings.Split(blob, ",")
		var proc, pid string
		if len(parts) > 0 {
			proc = strings.Trim(parts[0], `"`)
		}
		for _, p := range parts[1:] {
			p = strings.TrimSpace(p)
			if strings.HasPrefix(p, "pid=") {
				pid = strings.TrimPrefix(p, "pid=")
				break
			}
		}
		owners[port] = portOwner{Process: proc, PID: pid}
	}
	return owners
}

type timeSkewCheck struct{}

func (timeSkewCheck) Name() string { return "time-skew" }
func (timeSkewCheck) Run(ctx context.Context, plan HostPlan, r Runner) Result {
	// Sandwich the remote call between two local timestamps so that we can
	// compare against the midpoint of the local interval. This removes one-way
	// network latency from the measured skew.
	localBefore := time.Now().Unix()
	out, err := r.Output("date +%s")
	localAfter := time.Now().Unix()
	if err != nil {
		return Result{Name: "time-skew", Host: plan.Host, OK: false, Detail: err.Error()}
	}
	remote, err := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64)
	if err != nil {
		return Result{Name: "time-skew", Host: plan.Host, OK: false, Detail: err.Error()}
	}
	midpoint := (localBefore + localAfter) / 2
	diff := remote - midpoint
	if diff < 0 {
		diff = -diff
	}
	const toleranceSeconds int64 = 2
	if diff > toleranceSeconds {
		return Result{
			Name: "time-skew", Host: plan.Host, OK: false,
			Detail: fmt.Sprintf("clock skew %ds > %ds tolerance", diff, toleranceSeconds),
		}
	}
	return Result{
		Name: "time-skew", Host: plan.Host, OK: true,
		Detail: fmt.Sprintf("skew %ds (tolerance %ds)", diff, toleranceSeconds),
	}
}

type staleWeedCheck struct{}

func (staleWeedCheck) Name() string { return "stale-weed" }
func (staleWeedCheck) Run(ctx context.Context, plan HostPlan, r Runner) Result {
	out, err := r.Output("pgrep -x weed || true")
	if err != nil {
		return Result{Name: "stale-weed", Host: plan.Host, OK: true, Warn: true, Detail: err.Error()}
	}
	txt := strings.TrimSpace(string(out))
	if txt == "" {
		return Result{Name: "stale-weed", Host: plan.Host, OK: true, Detail: "no stale weed"}
	}
	return Result{
		Name: "stale-weed", Host: plan.Host, OK: true, Warn: true,
		Detail: "running weed pids: " + strings.ReplaceAll(txt, "\n", ","),
	}
}

type archOSCheck struct{}

func (archOSCheck) Name() string { return "arch-os" }
func (archOSCheck) Run(ctx context.Context, plan HostPlan, r Runner) Result {
	out, err := r.Output("uname -m; uname -s")
	if err != nil {
		return Result{Name: "arch-os", Host: plan.Host, OK: false, Detail: err.Error()}
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) < 2 {
		return Result{Name: "arch-os", Host: plan.Host, OK: false, Detail: "unexpected uname output"}
	}
	remoteArch := strings.TrimSpace(lines[0])
	remoteOS := strings.ToLower(strings.TrimSpace(lines[1]))

	if plan.Arch != "" && !archEqual(plan.Arch, remoteArch) {
		return Result{
			Name: "arch-os", Host: plan.Host, OK: false,
			Detail: fmt.Sprintf("arch mismatch: spec=%s remote=%s", plan.Arch, remoteArch),
		}
	}
	if plan.OS != "" && !strings.EqualFold(plan.OS, remoteOS) {
		return Result{
			Name: "arch-os", Host: plan.Host, OK: false,
			Detail: fmt.Sprintf("os mismatch: spec=%s remote=%s", plan.OS, remoteOS),
		}
	}
	return Result{
		Name: "arch-os", Host: plan.Host, OK: true,
		Detail: fmt.Sprintf("%s/%s", remoteOS, remoteArch),
	}
}

func archEqual(spec, remote string) bool {
	n := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		switch s {
		case "x86_64", "amd64":
			return "amd64"
		case "aarch64", "arm64":
			return "arm64"
		case "i386", "i686", "386":
			return "386"
		}
		return s
	}
	return n(spec) == n(remote)
}

// DefaultChecks returns the ordered list of checks executed by Run.
func DefaultChecks() []Check {
	return []Check{
		sshCheck{},
		sudoCheck{},
		diskSpaceCheck{},
		portsFreeCheck{},
		timeSkewCheck{},
		staleWeedCheck{},
		archOSCheck{},
	}
}

// Options customises a preflight run.
type Options struct {
	// DefaultSSHPort is the fallback SSH port for spec entries that do not set
	// one. Defaults to DefaultSSHPort.
	DefaultSSHPort int
	// Concurrency caps how many hosts are checked in parallel. Zero means
	// DefaultCheckConcurrency; negative values mean unlimited.
	Concurrency int
}

// Run executes every default check across every host in the spec using the
// default options. It is kept for backward compatibility with existing
// callers; prefer RunWithOptions for new code.
func Run(ctx context.Context, s *spec.Specification, factory SSHFactory) []Result {
	return RunWithOptions(ctx, s, factory, Options{})
}

// RunWithOptions executes every default check across every host in the spec
// and returns the full list of results, ordered by host. Hosts are processed
// concurrently up to opts.Concurrency. The SSH factory is used to open one
// Runner per host. If the factory itself fails, synthetic FAIL results are
// recorded against the affected host: a "ssh" failure for connectivity and
// "skipped" entries for every check that could not execute.
func RunWithOptions(ctx context.Context, s *spec.Specification, factory SSHFactory, opts Options) []Result {
	defaultSSHPort := opts.DefaultSSHPort
	if defaultSSHPort <= 0 {
		defaultSSHPort = DefaultSSHPort
	}
	concurrency := opts.Concurrency
	if concurrency == 0 {
		concurrency = DefaultCheckConcurrency
	}

	plans := BuildHostPlans(s, defaultSSHPort)
	checks := DefaultChecks()

	// Preserve host ordering in the output by writing into a per-host slot.
	perHost := make([][]Result, len(plans))

	g, gctx := errgroup.WithContext(ctx)
	if concurrency > 0 {
		g.SetLimit(concurrency)
	}
	var mu sync.Mutex // guards perHost writes (index writes are disjoint but
	// append-safety makes the intent explicit).
	for i, plan := range plans {
		i, plan := i, plan
		g.Go(func() error {
			var hostResults []Result
			err := factory(gctx, plan.Host, plan.SSHPort, func(r Runner) error {
				for _, c := range checks {
					hostResults = append(hostResults, c.Run(gctx, plan, r))
				}
				return nil
			})
			if err != nil {
				// Connection/auth failure: report as an "ssh" failure and
				// mark every remaining check as skipped so the report makes
				// it obvious that the sudo check did not actually run.
				hostResults = []Result{{
					Name: "ssh", Host: plan.Host, OK: false,
					Detail: "ssh connect failed: " + err.Error(),
				}}
				for _, c := range checks {
					if c.Name() == "ssh" {
						continue
					}
					hostResults = append(hostResults, Result{
						Name: c.Name(), Host: plan.Host, OK: false,
						Detail: "skipped: ssh unavailable",
					})
				}
			}
			mu.Lock()
			perHost[i] = hostResults
			mu.Unlock()
			return nil
		})
	}
	_ = g.Wait()

	var results []Result
	for _, hr := range perHost {
		results = append(results, hr...)
	}
	return results
}

// HasFailure reports whether any non-warn result failed.
func HasFailure(results []Result) bool {
	for _, r := range results {
		if !r.OK && !r.Warn {
			return true
		}
	}
	return false
}

// Pretty writes a human readable report using fatih/color.
func Pretty(w io.Writer, results []Result) {
	ok := color.New(color.FgGreen).SprintFunc()
	fail := color.New(color.FgRed).SprintFunc()
	warn := color.New(color.FgYellow).SprintFunc()

	// Group by host for readability.
	byHost := map[string][]Result{}
	var hosts []string
	for _, r := range results {
		if _, seen := byHost[r.Host]; !seen {
			hosts = append(hosts, r.Host)
		}
		byHost[r.Host] = append(byHost[r.Host], r)
	}
	sort.Strings(hosts)
	for _, h := range hosts {
		_, _ = fmt.Fprintf(w, "Host %s\n", h)
		for _, r := range byHost[h] {
			var tag string
			switch {
			case !r.OK:
				tag = fail("FAIL")
			case r.Warn:
				tag = warn("WARN")
			default:
				tag = ok("OK  ")
			}
			_, _ = fmt.Fprintf(w, "  [%s] %-12s %s\n", tag, r.Name, r.Detail)
		}
	}
}

// shellQuote returns a minimally quoted version of s safe for /bin/sh.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// --- SSH factory adapter ---

// OperatorSSHFactory builds an SSHFactory backed by pkg/operator.ExecuteRemote.
// Each host connection uses the provided credentials.
func OperatorSSHFactory(user, identityFile, password string) SSHFactory {
	return func(ctx context.Context, host string, port int, fn func(Runner) error) error {
		address := net.JoinHostPort(host, strconv.Itoa(port))
		return operator.ExecuteRemote(address, user, identityFile, password, func(op operator.CommandOperator) error {
			return fn(runnerFromOperator{op})
		})
	}
}

type runnerFromOperator struct{ op operator.CommandOperator }

func (r runnerFromOperator) Output(cmd string) ([]byte, error) { return r.op.Output(cmd) }
