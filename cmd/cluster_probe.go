package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
	"github.com/seaweedfs/seaweed-up/pkg/cluster/probe"
)

// ClusterProbeOptions holds flags for `cluster probe`.
type ClusterProbeOptions struct {
	InventoryFile string
	JSONOutput    bool
	Concurrency   int
}

func newClusterProbeCmd() *cobra.Command {
	opts := &ClusterProbeOptions{
		JSONOutput:  true,
		Concurrency: 10,
	}

	cmd := &cobra.Command{
		Use:   "probe",
		Short: "Probe hosts in an inventory for hardware facts",
		Long: `Probe SSHes into each host in the inventory, collects disks,
CPU, memory, and network facts, and prints them as JSON.

Intended both as a debugging tool (to see what 'cluster plan' would
observe) and as a scripting primitive. Purely read-only — no state
changes on the target hosts.

See docs/design/inventory-and-plan.md for the inventory schema.`,
		Example: `  seaweed-up cluster probe -i inventory.yaml
  seaweed-up cluster probe -i inventory.yaml --concurrency 20 > facts.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runClusterProbe(cmd, opts)
		},
	}

	cmd.Flags().StringVarP(&opts.InventoryFile, "inventory", "i", "", "inventory file (required)")
	cmd.Flags().BoolVar(&opts.JSONOutput, "json", true, "output as JSON (the only supported format today)")
	cmd.Flags().IntVar(&opts.Concurrency, "concurrency", 10, "max concurrent probes")
	_ = cmd.MarkFlagRequired("inventory")
	return cmd
}

func runClusterProbe(cmd *cobra.Command, opts *ClusterProbeOptions) error {
	inv, err := inventory.Load(opts.InventoryFile)
	if err != nil {
		return err
	}

	hosts := inv.ProbeHosts()
	if len(hosts) == 0 {
		return fmt.Errorf("no probeable hosts in %s (all entries are 'external'?)", opts.InventoryFile)
	}

	facts := make([]probe.HostFacts, len(hosts))

	// errgroup's context cancels on Ctrl+C / parent cancellation; propagate
	// it into each probe goroutine so queued-but-not-started probes bail
	// out instead of running against a doomed context.
	eg, ctx := errgroup.WithContext(cmd.Context())
	if opts.Concurrency > 0 {
		eg.SetLimit(opts.Concurrency)
	}

	// Progress goes to stderr so stdout stays a clean JSON document
	// pipeable into jq / further tooling.
	var progressMu sync.Mutex
	for i := range hosts {
		i := i
		h := hosts[i]
		eg.Go(func() error {
			// Queued behind SetLimit — check the context before doing
			// anything SSH so a cancelled run doesn't open new sessions.
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
	_ = eg.Wait()

	if opts.JSONOutput {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(facts)
	}
	// Non-JSON output is reserved for a future --table / --yaml mode.
	return fmt.Errorf("non-JSON output is not yet implemented")
}
