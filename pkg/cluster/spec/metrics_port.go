package spec

// MetricsPortBase is the first port handed out for a component's Prometheus
// metrics endpoint when one isn't set explicitly.
const MetricsPortBase = 9324

// AssignMetricsPorts gives every master/volume/filer without an explicit
// metrics_port a per-host-unique one (so two volume servers on the same host
// don't collide), starting at MetricsPortBase. Explicitly-set ports are
// preserved and reserved. It is idempotent — a second call is a no-op.
//
// This is the single source of truth for metrics-port assignment: both the
// deploy path (which feeds the result to weed's -metricsPort) and the
// rendered Prometheus scrape config run it, so the ports weed serves and the
// ports Prometheus scrapes always agree.
func AssignMetricsPorts(s *Specification) {
	if s == nil {
		return
	}
	used := map[string]map[int]bool{}
	mark := func(ip string, p int) {
		if p == 0 {
			return
		}
		if used[ip] == nil {
			used[ip] = map[int]bool{}
		}
		used[ip][p] = true
	}
	for _, m := range s.MasterServers {
		if m != nil {
			mark(m.Ip, m.MetricsPort)
		}
	}
	for _, v := range s.VolumeServers {
		if v != nil {
			mark(v.Ip, v.MetricsPort)
		}
	}
	for _, f := range s.FilerServers {
		if f != nil {
			mark(f.Ip, f.MetricsPort)
		}
	}

	next := map[string]int{}
	alloc := func(ip string) int {
		if used[ip] == nil {
			used[ip] = map[int]bool{}
		}
		p := next[ip]
		if p < MetricsPortBase {
			p = MetricsPortBase
		}
		for used[ip][p] {
			p++
		}
		used[ip][p] = true
		next[ip] = p + 1
		return p
	}
	for _, m := range s.MasterServers {
		if m != nil && m.MetricsPort == 0 {
			m.MetricsPort = alloc(m.Ip)
		}
	}
	for _, v := range s.VolumeServers {
		if v != nil && v.MetricsPort == 0 {
			v.MetricsPort = alloc(v.Ip)
		}
	}
	for _, f := range s.FilerServers {
		if f != nil && f.MetricsPort == 0 {
			f.MetricsPort = alloc(f.Ip)
		}
	}
}
