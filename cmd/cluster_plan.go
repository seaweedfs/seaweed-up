package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/plan"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

// Environment variable that, when set, supplies the filer metadata-store
// DSN. Takes precedence over `--filer-backend-file` only when both are
// absent (see resolveFilerBackendDSN).
const envFilerBackend = "SEAWEEDUP_FILER_BACKEND"

// ClusterPlanOptions holds flags for `cluster plan`.
//
// Two modes:
//  1. --json (default, Phase 1) — probe and emit HostFacts to stdout.
//  2. -o cluster.yaml (Phase 2, greenfield) — probe and synthesize a
//     reviewable cluster.yaml. Refuses to overwrite an existing file
//     unless --overwrite is passed; append-merge lands in Phase 3.
//
// See docs/design/inventory-and-plan.md for the full design.
type ClusterPlanOptions struct {
	InventoryFile string
	OutputFile    string
	Overwrite     bool
	JSONOutput    bool
	Concurrency   int

	ClusterName       string
	VolumeSizeLimitMB int
	FilerBackend      string
	FilerBackendFile  string
	VolumeServerShape string
}

func newClusterPlanCmd() *cobra.Command {
	opts := &ClusterPlanOptions{
		Concurrency: 10,
	}

	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Probe hosts in an inventory and (optionally) emit a reviewable cluster.yaml",
		Long: `Plan SSHes into each host in the inventory, collects disks, CPU,
memory, and network facts, and either:

  - emits those facts as JSON (--json, the default when -o is absent), or
  - synthesizes a reviewable cluster.yaml (-o cluster.yaml) that the
    existing ` + "`cluster deploy`" + ` command consumes unchanged.

Phase 2 lands the synthesis path in greenfield mode only: the command
refuses to overwrite an existing -o file unless --overwrite is passed.
Phase 3 will add append-merge so growing the inventory only appends to
the generated cluster.yaml without reordering or rewriting existing
entries. See docs/design/inventory-and-plan.md.

Purely read-only on the target hosts.`,
		Example: `  # Probe-only (JSON to stdout)
  seaweed-up cluster plan -i inventory.yaml > facts.json

  # Synthesize a cluster.yaml for review
  seaweed-up cluster plan -i inventory.yaml -o cluster.yaml \
      --filer-backend-file /etc/seaweed-up/filer.dsn

  # Overwrite an existing cluster.yaml (Phase 3 will replace this)
  seaweed-up cluster plan -i inventory.yaml -o cluster.yaml --overwrite`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterPlan(cmd, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.InventoryFile, "inventory", "i", "", "inventory file (required)")
	cmd.Flags().StringVarP(&opts.OutputFile, "output", "o", "", "write a synthesized cluster.yaml to this path")
	cmd.Flags().BoolVar(&opts.Overwrite, "overwrite", false, "overwrite -o if it already exists (Phase 3 will land append-merge)")
	cmd.Flags().BoolVar(&opts.JSONOutput, "json", false, "write probe facts as JSON to stdout (default when -o is absent)")
	cmd.Flags().IntVar(&opts.Concurrency, "concurrency", 10, "max concurrent probes")

	cmd.Flags().StringVar(&opts.ClusterName, "cluster-name", "", "cluster_name to stamp on the generated cluster.yaml")
	cmd.Flags().IntVar(&opts.VolumeSizeLimitMB, "volume-size-limit-mb", 0, "volumeSizeLimitMB for the generated global block (default 5000)")
	cmd.Flags().StringVar(&opts.FilerBackend, "filer-backend", "", "filer metadata DSN, e.g. postgres://user:pass@host/db (leaks via ps; prefer --filer-backend-file)")
	cmd.Flags().StringVar(&opts.FilerBackendFile, "filer-backend-file", "", "path to a file containing the filer metadata DSN")
	cmd.Flags().StringVar(&opts.VolumeServerShape, "volume-server-shape", "", "how to map disks to volume_server entries: per-host (default; one entry, all disks under folders:) or per-disk (one entry per disk, distinct ports)")

	_ = cmd.MarkFlagRequired("inventory")
	return cmd
}

func runClusterPlan(cmd *cobra.Command, opts *ClusterPlanOptions) error {
	inv, err := inventory.Load(opts.InventoryFile)
	if err != nil {
		return err
	}

	// If -o is unset and --json wasn't asked for explicitly, default to
	// JSON (preserves Phase 1 behavior). If -o is set, --json is
	// silently off (there's a single stdout; mixing them helps nobody).
	jsonOut := opts.JSONOutput
	if opts.OutputFile == "" && !opts.JSONOutput {
		jsonOut = true
	}
	if opts.OutputFile != "" && opts.JSONOutput {
		return fmt.Errorf("--json and -o are mutually exclusive; use one")
	}

	// Greenfield guard: refuse to overwrite an existing cluster.yaml or
	// its sidecar facts file until Phase 3 lands append-merge.
	// --overwrite opts out for hand-edits the operator has already
	// copied elsewhere.
	factsFile := factsFilePath(opts.OutputFile)
	if opts.OutputFile != "" && !opts.Overwrite {
		for _, p := range []string{opts.OutputFile, factsFile} {
			if _, statErr := os.Stat(p); statErr == nil {
				return fmt.Errorf("%s already exists; pass --overwrite to replace (append-merge lands in Phase 3)", p)
			}
		}
	}

	hosts := inv.ProbeHosts()
	if len(hosts) == 0 {
		return fmt.Errorf("no probeable hosts in %s (all entries are 'external'?)", opts.InventoryFile)
	}

	facts, err := probeAll(cmd, inv, hosts, opts.Concurrency)
	if err != nil {
		return err
	}

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(facts)
	}

	// Synthesis path needs the filer DSN. Resolve here (after the
	// json-only branch) so a stray $SEAWEEDUP_FILER_BACKEND in the
	// environment can't fail a probe-only run with a backend it
	// doesn't even need.
	backend, err := resolveFilerBackend(opts)
	if err != nil {
		return err
	}

	// Synthesize a cluster.yaml from the facts + inventory.
	factsByTarget := make(map[string]probe.HostFacts, len(facts))
	for _, f := range facts {
		factsByTarget[fmt.Sprintf("%s:%d", f.IP, f.SSHPort)] = f
	}
	spec, report, err := plan.Generate(inv, factsByTarget, plan.Options{
		ClusterName:       opts.ClusterName,
		VolumeSizeLimitMB: opts.VolumeSizeLimitMB,
		FilerBackend:      backend,
		VolumeServerShape: opts.VolumeServerShape,
	})
	if err != nil {
		return fmt.Errorf("generate cluster spec: %w", err)
	}
	printSkipReport(report)
	body, err := plan.Marshal(spec, plan.MarshalOptions{InventoryPath: opts.InventoryFile})
	if err != nil {
		return fmt.Errorf("marshal cluster spec: %w", err)
	}
	// The generated cluster.yaml may carry the filer metadata-store DSN
	// (username + password) in config:, so restrict to the deploying
	// user. Matches gosec G306 / CWE-276.
	if err := os.WriteFile(opts.OutputFile, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", opts.OutputFile, err)
	}

	// Write the probe facts as a sidecar JSON file alongside cluster.yaml.
	// Useful for debugging (operators can see what plan saw without
	// re-probing) and as audit / reproducibility input for Phase 3
	// append-merge. Same 0o600 — facts include hostnames, NIC addresses,
	// and disk model strings (host-enumeration data).
	factsBody, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal facts: %w", err)
	}
	factsBody = append(factsBody, '\n')
	if err := os.WriteFile(factsFile, factsBody, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", factsFile, err)
	}

	fmt.Fprintf(os.Stderr, "wrote %s (%d masters, %d volumes, %d filers)\nwrote %s (%d host facts)\n",
		opts.OutputFile, len(spec.MasterServers), len(spec.VolumeServers), len(spec.FilerServers),
		factsFile, len(facts))
	return nil
}

// factsFilePath derives the sidecar JSON path for a given cluster.yaml
// output. cluster.yaml -> cluster.facts.json, cluster.yml ->
// cluster.facts.json, anything-else -> anything-else.facts.json. Keeps
// the two artifacts adjacent so a directory listing makes the
// relationship obvious.
func factsFilePath(outputFile string) string {
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(outputFile, ext) {
			return outputFile[:len(outputFile)-len(ext)] + ".facts.json"
		}
	}
	return outputFile + ".facts.json"
}

// loadPlannedDisksFromFacts reads the facts.json sidecar that
// `cluster plan -o` writes alongside cluster.yaml and returns the
// per-host set of disk paths deploy is allowed to mkfs+mount.
// Returns nil when the sidecar is missing, unreadable, or empty —
// callers fall back to the historical "scan every prefix-matching
// disk" behavior. The disk-classification rules here mirror
// pkg/cluster/plan.deriveFolders so plan and deploy agree on which
// devices are eligible.
func loadPlannedDisksFromFacts(specPath string) map[string]map[string]struct{} {
	data, err := os.ReadFile(factsFilePath(specPath))
	if err != nil {
		return nil
	}
	var facts []probe.HostFacts
	if err := json.Unmarshal(data, &facts); err != nil {
		return nil
	}
	out := make(map[string]map[string]struct{})
	for _, h := range facts {
		if h.ProbeError != "" {
			continue
		}
		approved := make(map[string]struct{})
		for _, d := range h.Disks {
			if d.Ephemeral {
				continue
			}
			// Effective mountpoint: kernel's view first, fstab as
			// fallback. Same logic as plan's classifier.
			effective := d.MountPoint
			if effective == "" {
				effective = d.FstabMountPoint
			}
			if effective != "" {
				// Foreign mounts (anything not /data\d+) are excluded
				// from the allowlist. Cluster-claimed /dataN mounts are
				// approved — deploy will idempotently leave them be.
				if !strings.HasPrefix(effective, "/data") {
					continue
				}
				approved[d.Path] = struct{}{}
				continue
			}
			if d.FSType != "" {
				// Has a filesystem but no claim — plan skipped it,
				// deploy should too.
				continue
			}
			approved[d.Path] = struct{}{}
		}
		if len(approved) > 0 {
			out[h.IP] = approved
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// printSkipReport surfaces Generate's skip decisions to stderr so the
// operator isn't left wondering why a host they put in the inventory
// didn't appear in cluster.yaml. Quiet when Report is zero-valued.
func printSkipReport(report plan.Report) {
	if len(report.ProbeFailed) > 0 {
		_, _ = color.New(color.FgYellow).Fprintln(os.Stderr, "skipped hosts (probe failed):")
		for _, f := range report.ProbeFailed {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", f.IP, f.Reason)
		}
	}
	if len(report.VolumeHostsNoDisks) > 0 {
		_, _ = color.New(color.FgYellow).Fprintln(os.Stderr, "dropped volume role (no eligible free disks):")
		for _, ip := range report.VolumeHostsNoDisks {
			fmt.Fprintf(os.Stderr, "  %s\n", ip)
		}
	}
	if len(report.EphemeralDisksSkipped) > 0 {
		_, _ = color.New(color.FgYellow).Fprintln(os.Stderr, "skipped ephemeral disks (set defaults.disk.allow_ephemeral: true to override):")
		for _, e := range report.EphemeralDisksSkipped {
			fmt.Fprintf(os.Stderr, "  %s: %s\n", e.IP, strings.Join(e.Disks, ", "))
		}
	}
}

// resolveFilerBackend picks the filer DSN from (in priority order)
// --filer-backend-file, --filer-backend, SEAWEEDUP_FILER_BACKEND. Zero
// return value (nil) is fine — the planner emits a placeholder comment
// instead of a config block.
func resolveFilerBackend(opts *ClusterPlanOptions) (map[string]interface{}, error) {
	var dsn string
	switch {
	case opts.FilerBackendFile != "":
		data, err := os.ReadFile(opts.FilerBackendFile)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", opts.FilerBackendFile, err)
		}
		dsn = strings.TrimSpace(string(data))
	case opts.FilerBackend != "":
		dsn = opts.FilerBackend
	default:
		dsn = strings.TrimSpace(os.Getenv(envFilerBackend))
	}
	if dsn == "" {
		return nil, nil
	}
	backend, err := plan.ParseFilerBackendDSN(dsn)
	if err != nil {
		return nil, fmt.Errorf("filer backend: %w", err)
	}
	return backend, nil
}

// probeAll fans the probe across hosts with the concurrency cap and
// surfaces any context-cancellation error so a Ctrl+C run exits
// non-zero without emitting a partial facts slice.
func probeAll(cmd *cobra.Command, inv *inventory.Inventory, hosts []*inventory.Host, concurrency int) ([]probe.HostFacts, error) {
	facts := make([]probe.HostFacts, len(hosts))

	eg, ctx := errgroup.WithContext(cmd.Context())
	if concurrency > 0 {
		eg.SetLimit(concurrency)
	}

	var progressMu sync.Mutex
	for i := range hosts {
		i := i
		h := hosts[i]
		eg.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			facts[i] = probe.Probe(inv, h)

			progressMu.Lock()
			defer progressMu.Unlock()
			if facts[i].ProbeError != "" {
				_, _ = color.New(color.FgRed).Fprintf(os.Stderr, "  probing %s ... FAIL: %s\n", h.IP, facts[i].ProbeError)
			} else {
				fmt.Fprintf(os.Stderr, "  probing %s ... ok (%d cores, %d disks)\n",
					h.IP, facts[i].CPUCores, len(facts[i].Disks))
			}
			return nil
		})
	}
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return facts, nil
}
