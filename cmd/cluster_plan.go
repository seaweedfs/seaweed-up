package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/plan"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

// envFilerBackend is the environment variable name that supplies the
// filer metadata-store DSN. It is the lowest-priority source: used
// only when neither `--filer-backend-file` nor `--filer-backend` is
// passed. See resolveFilerBackend; precedence is file > flag > env,
// matching the design doc and cobra/viper's flag-overrides-env
// convention.
const envFilerBackend = "SEAWEEDUP_FILER_BACKEND"

// ClusterPlanOptions holds flags for `cluster plan`.
//
// Two modes:
//  1. --json (default when -o is absent) — probe and emit HostFacts
//     to stdout.
//  2. -o cluster.yaml — probe and synthesize a reviewable
//     cluster.yaml. When the file exists the run append-merges in
//     place, preserving comments and operator hand-edits;
//     --overwrite regenerates from scratch.
//
// See docs/design/inventory-and-plan.md for the full design.
type ClusterPlanOptions struct {
	InventoryFile string
	OutputFile    string
	Overwrite     bool
	JSONOutput    bool
	// DryRun runs the full probe + synthesis pipeline but writes
	// nothing. Instead it prints a unified diff between the existing
	// -o file (or empty for greenfield) and the body plan would write.
	// Implies -o (no diff target makes no sense without it). Sidecars
	// are reported as "would write" lines but not actually written.
	DryRun      bool
	Concurrency int

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

When -o points at an existing cluster.yaml the run append-merges in
place: new inventory hosts are appended at each section's tail and
existing entries (along with operator hand-edits and comments) are
preserved byte-for-byte. Pass --overwrite to regenerate from scratch
instead. Pass --dry-run to print a unified diff of what -o would
change without writing anything. See docs/design/inventory-and-plan.md.

Purely read-only on the target hosts.`,
		Example: `  # Probe-only (JSON to stdout)
  seaweed-up cluster plan -i inventory.yaml > facts.json

  # Synthesize a cluster.yaml for review (greenfield or append-merge)
  seaweed-up cluster plan -i inventory.yaml -o cluster.yaml \
      --filer-backend-file /etc/seaweed-up/filer.dsn

  # Preview what plan would change without writing anything
  seaweed-up cluster plan -i inventory.yaml -o cluster.yaml --dry-run

  # Force regeneration, discarding any existing cluster.yaml
  seaweed-up cluster plan -i inventory.yaml -o cluster.yaml --overwrite`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterPlan(cmd, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.InventoryFile, "inventory", "i", "", "inventory file (required)")
	cmd.Flags().StringVarP(&opts.OutputFile, "output", "o", "", "write a synthesized cluster.yaml to this path")
	cmd.Flags().BoolVar(&opts.Overwrite, "overwrite", false, "regenerate -o from scratch instead of append-merging into the existing file")
	cmd.Flags().BoolVar(&opts.JSONOutput, "json", false, "write probe facts as JSON to stdout (default when -o is absent)")
	cmd.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print a unified diff of what -o would change instead of writing it (requires -o)")
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
	if opts.DryRun && opts.OutputFile == "" {
		// --dry-run prints a diff of what -o would change. Without -o
		// there's no diff target and nothing to preview — refuse with
		// a clear error rather than silently turning into a probe-only
		// run that ignores the flag.
		return fmt.Errorf("--dry-run requires -o; pass the path you'd like to preview against")
	}

	// Three modes for `-o cluster.yaml`:
	//   1. file does not exist        → greenfield (yaml.Marshal)
	//   2. file exists, no --overwrite → append-merge (preserve bytes)
	//   3. file exists, --overwrite    → regenerate from scratch
	// Sidecars (facts.json, deploy-disks.json) are always rewritten
	// from the latest probe — they're audit + deploy contracts, not
	// hand-edit surfaces, so byte-stability isn't a goal there.
	factsFile := factsFilePath(opts.OutputFile)
	deployDisksFile := deployDisksFilePath(opts.OutputFile)
	mergeMode := false
	if opts.OutputFile != "" && !opts.Overwrite {
		// Discriminate ErrNotExist (legitimate greenfield) from other
		// stat failures (EACCES, EIO, NFS hiccup, …). Treating every
		// non-nil error as "no file" would silently fall back to the
		// greenfield path and the later WriteFile would clobber the
		// hand-edited cluster.yaml as soon as access was restored —
		// the exact data-loss path append-merge exists to prevent.
		_, statErr := os.Stat(opts.OutputFile)
		switch {
		case statErr == nil:
			mergeMode = true
		case errors.Is(statErr, fs.ErrNotExist):
			// Greenfield path; nothing to merge into.
		default:
			return fmt.Errorf("stat %s: %w", opts.OutputFile, statErr)
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
		factsByTarget[plan.SSHTargetKey(f.IP, f.SSHPort)] = f
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
	// Compute the deploy allowlist alongside the spec so plan and
	// deploy share one source of truth on disk eligibility (including
	// inventory excludes and ephemeral filtering).
	allowlist := plan.EligibleDisks(inv, factsByTarget)
	printSkipReport(report)

	var (
		body        []byte
		mergeReport *plan.MergeReport
		existing    []byte // populated in mergeMode; reused by --dry-run
	)
	if mergeMode {
		// Append-merge into the existing file. Merge() guarantees
		// byte-stable output for unchanged inventory; new hosts are
		// appended at each section's tail and orphans surface in
		// mergeReport without mutating existing entries. (Drift
		// detection lands in Phase 4 alongside --refresh-host.)
		var readErr error
		existing, readErr = os.ReadFile(opts.OutputFile)
		if readErr != nil {
			return fmt.Errorf("read existing %s: %w", opts.OutputFile, readErr)
		}
		body, mergeReport, err = plan.Merge(existing, spec, plan.MergeOptions{
			Marshal: plan.MarshalOptions{InventoryPath: opts.InventoryFile},
		})
		if err != nil {
			return fmt.Errorf("merge into existing cluster spec: %w", err)
		}
	} else {
		body, err = plan.Marshal(spec, plan.MarshalOptions{InventoryPath: opts.InventoryFile})
		if err != nil {
			return fmt.Errorf("marshal cluster spec: %w", err)
		}
	}
	// In --dry-run mode, render a unified diff to stdout against the
	// existing -o (or empty for greenfield / missing file) and exit
	// without touching any file. The sidecars are summarized but not
	// written so the preview is purely read-only on disk.
	if opts.DryRun {
		// In merge mode `existing` was already loaded above; reuse it
		// so the diff bytes match the bytes Merge consumed (no TOCTOU
		// window between the two reads). Outside merge mode (greenfield
		// or --overwrite path) read here, tolerating ErrNotExist as the
		// legitimate greenfield case. Any other read failure surfaces
		// so an EACCES doesn't silently degrade the preview.
		if !mergeMode {
			loaded, readErr := os.ReadFile(opts.OutputFile)
			if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
				return fmt.Errorf("read existing %s: %w", opts.OutputFile, readErr)
			}
			existing = loaded
		}
		diff := plan.UnifiedDiff(opts.OutputFile, existing, body)
		if diff == "" {
			fmt.Fprintf(os.Stderr, "no changes — %s would be byte-identical to the existing file\n", opts.OutputFile)
		} else {
			fmt.Print(diff)
		}
		fmt.Fprintf(os.Stderr,
			"dry-run: would write %s (%d masters, %d volumes, %d filers), %s (%d host facts), %s (%d targets, %d eligible disks)\n",
			opts.OutputFile, len(spec.MasterServers), len(spec.VolumeServers), len(spec.FilerServers),
			factsFile, len(facts),
			deployDisksFile, len(allowlist), countAllowlistDisks(allowlist))
		printMergeReport(mergeReport)
		return nil
	}

	// The generated cluster.yaml may carry the filer metadata-store DSN
	// (username + password) in config:, so restrict to the deploying
	// user. Matches gosec G306 / CWE-276.
	//
	// G703 fires on this line because in merge mode we ALSO read from
	// opts.OutputFile a few lines above; gosec's taint analysis then
	// flags writing back to the same path as a potential traversal.
	// The path is the operator's own --output flag — there's no
	// untrusted data flow here. Suppress the false positive.
	// #nosec G703 -- opts.OutputFile is a user-supplied CLI flag
	if err := os.WriteFile(opts.OutputFile, body, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", opts.OutputFile, err)
	}

	// Write the probe facts as a sidecar JSON file alongside cluster.yaml.
	// Useful for debugging (operators can see what plan saw without
	// re-probing) and as an audit record for the next append-merge run.
	// Same 0o600 — facts include hostnames, NIC addresses, and disk
	// model strings (host-enumeration data).
	factsBody, err := json.MarshalIndent(facts, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal facts: %w", err)
	}
	factsBody = append(factsBody, '\n')
	if err := os.WriteFile(factsFile, factsBody, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", factsFile, err)
	}

	// Write the explicit deploy-disks allowlist next to the spec.
	// `cluster deploy` reads this in preference to reconstructing
	// from facts.json so inventory excludes and the ephemeral skip
	// flow through unambiguously. deployDisksFile path was already
	// computed (and overwrite-checked) above.
	allowBody, err := json.MarshalIndent(allowlist, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal deploy disks: %w", err)
	}
	allowBody = append(allowBody, '\n')
	if err := os.WriteFile(deployDisksFile, allowBody, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", deployDisksFile, err)
	}

	// Headline counts come from the freshly-Generated spec, which
	// reflects current inventory only. In merge mode the on-disk file
	// can also carry orphan entries that won't show up in `spec`, so
	// the counts would under-report what cluster.yaml actually
	// contains. Disambiguate the wording by mode: greenfield = file
	// contents (counts match); merge = inventory contents (orphans
	// show up separately in printMergeReport's WARN lines).
	headline := "wrote %s (%d masters, %d volumes, %d filers)"
	if mergeMode {
		headline = "merged %s (inventory: %d masters, %d volumes, %d filers)"
	}
	fmt.Fprintf(os.Stderr, headline+"\nwrote %s (%d host facts)\nwrote %s (%d targets, %d eligible disks)\n",
		opts.OutputFile, len(spec.MasterServers), len(spec.VolumeServers), len(spec.FilerServers),
		factsFile, len(facts),
		deployDisksFile, len(allowlist), countAllowlistDisks(allowlist))
	printMergeReport(mergeReport) // no-op when nil (greenfield path)
	return nil
}

// printMergeReport surfaces append-merge outcomes the operator should
// see: appended new hosts and orphan entries (in the YAML but not
// produced by this plan run — removed from inventory, probe failed,
// or role was dropped because no eligible disks were found). All go
// to stderr to keep stdout reserved for --json mode and because
// they're advisory. Drift detection (warn when a previously-emitted
// entry's facts changed) is deferred to Phase 4 alongside
// `--refresh-host`.
func printMergeReport(r *plan.MergeReport) {
	if r == nil {
		return
	}
	// Sort section names so the per-section "appended to …" lines
	// come out in a stable order. Iterating r.Appended directly would
	// pick up Go's randomized map order, making the operator-facing
	// output non-deterministic across runs.
	sections := make([]string, 0, len(r.Appended))
	for s := range r.Appended {
		sections = append(sections, s)
	}
	sort.Strings(sections)
	for _, section := range sections {
		keys := r.Appended[section]
		if len(keys) == 0 {
			continue
		}
		fmt.Fprintf(os.Stderr, "  appended to %s: %s\n", section, strings.Join(keys, ", "))
	}
	for _, o := range r.Orphaned {
		fmt.Fprintf(os.Stderr, "  WARN: orphan in cluster.yaml (not produced by this plan run — removed from inventory, probe failed, or role was dropped): %s\n", o)
	}
	for _, u := range r.Unparseable {
		fmt.Fprintf(os.Stderr, "  WARN: unparseable existing entry — fresh inventory hosts won't dedupe against it: %s\n", u)
	}
}

func countAllowlistDisks(a plan.DeployDiskAllowlist) int {
	n := 0
	for _, paths := range a {
		n += len(paths)
	}
	return n
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

// deployDisksFilePath derives the explicit allowlist sidecar path
// that `cluster plan -o` writes alongside cluster.yaml. Unlike
// facts.json (raw probe data), this file carries plan's actual
// classification result — including the inventory's
// defaults.disk.exclude rules — so deploy can honor every skip plan
// made without re-deriving (and risking divergence from) the same
// logic.
func deployDisksFilePath(outputFile string) string {
	for _, ext := range []string{".yaml", ".yml"} {
		if strings.HasSuffix(outputFile, ext) {
			return outputFile[:len(outputFile)-len(ext)] + ".deploy-disks.json"
		}
	}
	return outputFile + ".deploy-disks.json"
}

// loadPlannedDeployDisks reads the deploy-disks.json sidecar `cluster
// plan -o` writes alongside cluster.yaml and returns the per-SSH-target
// set of disk paths deploy is allowed to mkfs+mount.
//
// Fail-closed semantics: a plan-generated cluster.yaml is detected by
// the planGeneratedMarker comment plan stamps onto its first line.
// The marker travels with the file, so an operator who scp's just
// cluster.yaml (no sidecars) still gets fail-closed treatment instead
// of silent fallback to deploy's legacy scan-everything path. When
// the spec is plan-generated but the deploy-disks sidecar is missing,
// unreadable, or unparseable we return an error.
//
// (nil, nil) means "hand-written cluster.yaml — no marker, no
// sidecars expected, legacy scan-everything is correct".
// (map, nil) carries the allowlist (possibly empty for clusters with
// no volume hosts).
// (nil, err) means "plan-generated but sidecar is missing or broken —
// refuse to deploy".
func loadPlannedDeployDisks(specPath string) (map[string]map[string]struct{}, error) {
	sidecar := deployDisksFilePath(specPath)
	sidecarExists := fileExists(sidecar)
	planGenerated, _ := isPlanGeneratedSpec(specPath)

	if !planGenerated && !sidecarExists {
		// Hand-written cluster.yaml: no marker, no sidecar — legacy
		// scan-everything behavior is intentional.
		return nil, nil
	}
	if planGenerated && !sidecarExists {
		return nil, fmt.Errorf(
			"%s carries the %q marker (cluster.yaml is plan-generated) but %s is missing — "+
				"re-run `cluster plan -i <inventory> -o %s --overwrite` to regenerate "+
				"the disk allowlist; refusing to fall back to scanning all disks",
			specPath, plan.PlanGeneratedMarker, sidecar, specPath)
	}
	// sidecar exists but no marker (operator hand-wrote a deploy-disks
	// for a hand-written spec? unlikely but legal) — read it anyway.
	data, err := os.ReadFile(sidecar)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", sidecar, err)
	}
	var allow plan.DeployDiskAllowlist
	if err := json.Unmarshal(data, &allow); err != nil {
		return nil, fmt.Errorf("parse %s: %w", sidecar, err)
	}

	// The map can legitimately be empty (cluster with no volume hosts).
	// Always return a non-nil map when the sidecar parsed cleanly so
	// the manager applies the (possibly empty) allowlist
	// authoritatively rather than falling back.
	out := make(map[string]map[string]struct{}, len(allow))
	for target, paths := range allow {
		set := make(map[string]struct{}, len(paths))
		for _, p := range paths {
			set[p] = struct{}{}
		}
		out[target] = set
	}
	return out, nil
}

// isPlanGeneratedSpec returns true when the file at specPath was
// emitted by `cluster plan -o`. Detection is by header marker
// (planGeneratedMarker on the first non-blank line) so the signal
// travels with the file: an operator who copies just cluster.yaml
// without its sidecars still gets recognized as plan-generated.
func isPlanGeneratedSpec(specPath string) (bool, error) {
	data, err := os.ReadFile(specPath)
	if err != nil {
		return false, err
	}
	// Marker is on the first line; only inspect the prefix to avoid
	// pulling the whole file into a string compare.
	head := data
	if len(head) > 256 {
		head = head[:256]
	}
	return strings.Contains(string(head), plan.PlanGeneratedMarker), nil
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
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
