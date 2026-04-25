package plan

import (
	"bytes"
	"fmt"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/spec"
)

// MergeReport summarizes what append-merge did to an existing
// cluster.yaml. Always returned alongside the merged bytes; the cmd
// layer prints it so operators see appended hosts and orphan warnings
// without having to diff the file themselves.
type MergeReport struct {
	// Appended is the per-section list of `ip:port` entries that were
	// freshly appended to the document because the inventory carried
	// hosts the existing cluster.yaml didn't. Keys are section names
	// (`master_servers`, `volume_servers`, …); values are the new
	// entry keys in append order.
	Appended map[string][]string
	// Orphaned lists `section: ip:port` entries that exist in the YAML
	// but no longer appear in the inventory at the role's default port.
	// Append-merge never deletes — orphans surface as a warning so the
	// operator can decide.
	Orphaned []string
}

// MergeOptions tweak append-merge behavior. Zero value is fine for
// production use; tests pass MarshalOptions through for deterministic
// header re-stamping.
type MergeOptions struct {
	// Marshal carries the same fields as MarshalOptions and is forwarded
	// through to the greenfield Marshal call when the existing file is
	// empty / parses as an empty document. (A truly empty -o file is
	// indistinguishable from "this is the first plan run", so we fall
	// back to greenfield generation in that case.)
	Marshal MarshalOptions
}

// Merge appends entries from `fresh` into `existing`, preserving every
// existing byte that isn't part of an append. The contract:
//
//   - No-op run (inventory unchanged): output bytes equal input bytes
//     byte-for-byte. Operator hand-edits, comments, key ordering, and
//     style are preserved exactly.
//   - Append run (inventory has +1 host): the textual diff is exactly
//     a new mapping block at the right *_servers section's tail.
//   - User-edit survival: an operator who tightened `max: 200` on an
//     existing volume entry sees that edit retained.
//
// The implementation parses `existing` into a *yaml.Node tree (NEVER
// re-encodes it via yaml.Marshal of the spec struct) and only mutates
// the tree to append new sequence items. Existing nodes are byte-stable
// because yaml.v3 preserves head/line/foot comments, key order, and
// inline-vs-block style across a parse → encode round trip.
//
// When `existing` is empty, contains only comments, or has no document
// node, Merge falls back to greenfield Marshal — there's nothing to
// preserve.
func Merge(existing []byte, fresh *spec.Specification, opts MergeOptions) ([]byte, *MergeReport, error) {
	if fresh == nil {
		return nil, nil, fmt.Errorf("fresh spec is nil")
	}
	report := &MergeReport{Appended: map[string][]string{}}

	// Empty / whitespace-only / comment-only file: treat as greenfield.
	// yaml.v3 returns Document with Content==nil for these inputs; the
	// merge logic would have nothing to walk.
	trimmed := bytes.TrimSpace(existing)
	if len(trimmed) == 0 {
		body, err := Marshal(fresh, opts.Marshal)
		return body, report, err
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return nil, nil, fmt.Errorf("parse existing cluster.yaml: %w", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		// Comment-only document — no top-level mapping to merge into.
		body, err := Marshal(fresh, opts.Marshal)
		return body, report, err
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("unexpected top-level YAML kind %d (want mapping)", root.Kind)
	}

	// Each *_servers section is keyed differently in inventory vs. spec
	// (e.g. `master_servers`), but both ends agree on the YAML key. The
	// per-section helpers below own the marshalling of fresh entries to
	// yaml.Node fragments.
	// Per-section plan: section name + how to key fresh entries + how
	// to key existing entries. Most sections key on ip:port, but
	// worker/envoy have no single service port — they dedupe on ip
	// alone with a role-suffix sentinel so the index doesn't collapse
	// rows from different sections.
	type sectionPlan struct {
		key       string
		entries   []serverEntry
		keyOfNode func(node *yaml.Node) string
	}
	plans := []sectionPlan{
		{key: "master_servers", entries: masterEntries(fresh.MasterServers), keyOfNode: ipPortKeyOfNode},
		{key: "volume_servers", entries: volumeEntries(fresh.VolumeServers), keyOfNode: ipPortKeyOfNode},
		{key: "filer_servers", entries: filerEntries(fresh.FilerServers), keyOfNode: ipPortKeyOfNode},
		{key: "s3_servers", entries: s3Entries(fresh.S3Servers), keyOfNode: ipPortKeyOfNode},
		{key: "sftp_servers", entries: sftpEntries(fresh.SftpServers), keyOfNode: ipPortKeyOfNode},
		{key: "admin_servers", entries: adminEntries(fresh.AdminServers), keyOfNode: ipPortKeyOfNode},
		{key: "envoy_servers", entries: envoyEntries(fresh.EnvoyServers), keyOfNode: ipSentinelKeyFn("envoy")},
		{key: "worker_servers", entries: workerEntries(fresh.WorkerServers), keyOfNode: ipSentinelKeyFn("worker")},
	}

	for _, p := range plans {
		if err := mergeSection(root, p.key, p.entries, p.keyOfNode, report); err != nil {
			return nil, nil, fmt.Errorf("merge %s: %w", p.key, err)
		}
	}

	// Re-encode. yaml.v3's encoder preserves comments and styles on the
	// existing nodes; appended nodes use whatever style they were
	// marshalled with (block by default for mappings, which matches the
	// rest of the document).
	//
	// Detect the input document's indent so re-encoding doesn't
	// re-flow a hand-written 2-space file into 4-space. yaml.v3
	// applies one global indent setting; using a different one would
	// touch every existing node. Greenfield Marshal output is 4-space
	// (yaml.v3 default), so a freshly-generated file round-trips
	// stably regardless.
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(detectIndent(existing, 4))
	if err := enc.Encode(&doc); err != nil {
		return nil, nil, fmt.Errorf("encode merged cluster.yaml: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, nil, fmt.Errorf("close encoder: %w", err)
	}

	// yaml.v3 strips the head comment on the document node when the
	// node tree is rebuilt from scratch, but here we mutate in place,
	// so the original header (PlanGeneratedMarker, NOTE blocks, blank
	// lines) survives. No re-stamping needed in merge mode.
	return buf.Bytes(), report, nil
}

// detectIndent returns the leading-space width of the first nested
// list/map item in raw — typically 2 or 4 in a hand-written
// cluster.yaml. Falls back to fallback when no nested item is found
// (file empty, all flat, all comments). Clamped to [1,8] so a
// pathological input can't blow up the encoder.
//
// Heuristic: scan for the first line of the form `<spaces><dash> ` or
// `<spaces><alnum>:` where <spaces> is at least one space. yaml.v3
// uses the same indent for both sequence items and nested mappings,
// so either pattern reveals the file's chosen width.
func detectIndent(raw []byte, fallback int) int {
	for _, line := range bytes.Split(raw, []byte("\n")) {
		// Skip blank and comment-only lines so a `#` block in column 1
		// doesn't pin us to indent=0.
		trimmedLeft := bytes.TrimLeft(line, " ")
		if len(trimmedLeft) == 0 || trimmedLeft[0] == '#' {
			continue
		}
		spaces := len(line) - len(trimmedLeft)
		if spaces == 0 {
			continue
		}
		// Sequence dash or `key:` — both are valid indent witnesses.
		if trimmedLeft[0] == '-' || isYAMLKeyStart(trimmedLeft) {
			if spaces < 1 {
				spaces = 1
			}
			if spaces > 8 {
				spaces = 8
			}
			return spaces
		}
	}
	return fallback
}

// isYAMLKeyStart reports whether b looks like a `key:` token: starts
// with an alpha/digit/underscore byte followed by anything ending in
// `:`. We don't try to validate the full YAML key grammar — just
// enough to distinguish keys from raw scalar values that happen to
// start with whitespace.
func isYAMLKeyStart(b []byte) bool {
	if len(b) == 0 {
		return false
	}
	c := b[0]
	if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
		return false
	}
	return bytes.IndexByte(b, ':') > 0
}

// serverEntry is the unit of append: a key (`ip:port`) and the
// pre-marshalled yaml.Node for the spec entry. Building the node up
// front keeps mergeSection's logic shape-agnostic.
type serverEntry struct {
	key  string
	node *yaml.Node
}

// mergeSection finds the `*_servers` mapping under root and appends
// any entry whose key isn't already present. Records appends and
// orphans into report. keyOfNode extracts the dedup key from an
// existing sequence item (per-section because worker/envoy don't have
// a single service port).
func mergeSection(root *yaml.Node, sectionKey string, fresh []serverEntry, keyOfNode func(*yaml.Node) string, report *MergeReport) error {
	seqNode := findOrCreateSection(root, sectionKey)
	if seqNode == nil {
		// Section absent and no fresh entries: nothing to do.
		if len(fresh) == 0 {
			return nil
		}
		// Create the section as a fresh sequence and append into it.
		seqNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: sectionKey},
			seqNode,
		)
	}

	// Index existing entries by section-specific key. Skip entries we
	// can't extract a key from rather than failing the whole merge —
	// operators may have hand-edited weird shapes we don't recognize.
	existingKeys := map[string]struct{}{}
	for _, item := range seqNode.Content {
		if k := keyOfNode(item); k != "" {
			existingKeys[k] = struct{}{}
		}
	}

	freshKeys := map[string]struct{}{}
	for _, e := range fresh {
		freshKeys[e.key] = struct{}{}
		if _, ok := existingKeys[e.key]; ok {
			// Already present; never touch. (Drift detection lives
			// outside Merge; the cmd layer can compare reports.)
			continue
		}
		seqNode.Content = append(seqNode.Content, e.node)
		report.Appended[sectionKey] = append(report.Appended[sectionKey], e.key)
	}

	// Orphan check: existing keys not in inventory anymore.
	var orphans []string
	for k := range existingKeys {
		if _, ok := freshKeys[k]; !ok {
			orphans = append(orphans, fmt.Sprintf("%s: %s", sectionKey, k))
		}
	}
	sort.Strings(orphans) // deterministic for goldens / human reading
	report.Orphaned = append(report.Orphaned, orphans...)

	return nil
}

// findOrCreateSection scans root's mapping for sectionKey. Returns the
// associated sequence node when found, nil when absent (caller decides
// whether to create one).
func findOrCreateSection(root *yaml.Node, sectionKey string) *yaml.Node {
	for i := 0; i < len(root.Content); i += 2 {
		k := root.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == sectionKey {
			v := root.Content[i+1]
			// Tolerate `master_servers:` (null) as an empty section by
			// rewriting it in place to an empty sequence — append-mode
			// then has somewhere to put the new entries.
			if v.Kind == yaml.ScalarNode && (v.Tag == "!!null" || v.Value == "") {
				v.Kind = yaml.SequenceNode
				v.Tag = "!!seq"
				v.Value = ""
				v.Content = nil
			}
			return v
		}
	}
	return nil
}

// ipPortKeyOfNode extracts `ip:port` from a sequence item. The item
// must be a mapping with both `ip` and `port` scalar values; any other
// shape returns "" so mergeSection skips it.
func ipPortKeyOfNode(node *yaml.Node) string {
	if node == nil || node.Kind != yaml.MappingNode {
		return ""
	}
	var ip, port string
	for i := 0; i+1 < len(node.Content); i += 2 {
		k := node.Content[i]
		v := node.Content[i+1]
		if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode {
			continue
		}
		switch k.Value {
		case "ip":
			ip = v.Value
		case "port":
			port = v.Value
		}
	}
	if ip == "" || port == "" {
		return ""
	}
	return ip + ":" + port
}

// ipSentinelKeyFn returns a keyOfNode that dedupes on ip alone with a
// section-specific suffix (worker, envoy). Workers and envoys lack a
// single service port — admin port for workers, multi-protocol for
// envoy — so ip:<sentinel> is the stable dedup key. Inventory schema
// disallows duplicate (ip, role) entries, so per-IP keying is safe
// within a section.
func ipSentinelKeyFn(suffix string) func(*yaml.Node) string {
	return func(node *yaml.Node) string {
		if node == nil || node.Kind != yaml.MappingNode {
			return ""
		}
		for i := 0; i+1 < len(node.Content); i += 2 {
			k := node.Content[i]
			v := node.Content[i+1]
			if k.Kind != yaml.ScalarNode || v.Kind != yaml.ScalarNode {
				continue
			}
			if k.Value == "ip" && v.Value != "" {
				return v.Value + ":" + suffix
			}
		}
		return ""
	}
}

// --- per-section entry builders -------------------------------------------
//
// Each helper marshals one spec slice into serverEntry values: an ip:port
// key + the yaml.Node fragment to append. Marshalling goes through
// yaml.Marshal so the field order, omitempty, and scalar style match
// what greenfield Marshal would emit — appended entries are
// indistinguishable from same-section entries written on first run.

func masterEntries(ms []*spec.MasterServerSpec) []serverEntry {
	out := make([]serverEntry, 0, len(ms))
	for _, m := range ms {
		if m == nil {
			continue
		}
		node, err := specToYAMLNode(m)
		if err != nil {
			continue
		}
		out = append(out, serverEntry{key: keyOf(m.Ip, m.Port), node: node})
	}
	return out
}

func volumeEntries(vs []*spec.VolumeServerSpec) []serverEntry {
	out := make([]serverEntry, 0, len(vs))
	for _, v := range vs {
		if v == nil {
			continue
		}
		node, err := specToYAMLNode(v)
		if err != nil {
			continue
		}
		out = append(out, serverEntry{key: keyOf(v.Ip, v.Port), node: node})
	}
	return out
}

func filerEntries(fs []*spec.FilerServerSpec) []serverEntry {
	out := make([]serverEntry, 0, len(fs))
	for _, f := range fs {
		if f == nil {
			continue
		}
		node, err := specToYAMLNode(f)
		if err != nil {
			continue
		}
		out = append(out, serverEntry{key: keyOf(f.Ip, f.Port), node: node})
	}
	return out
}

func s3Entries(ss []*spec.S3ServerSpec) []serverEntry {
	out := make([]serverEntry, 0, len(ss))
	for _, s := range ss {
		if s == nil {
			continue
		}
		node, err := specToYAMLNode(s)
		if err != nil {
			continue
		}
		out = append(out, serverEntry{key: keyOf(s.Ip, s.Port), node: node})
	}
	return out
}

func sftpEntries(ss []*spec.SftpServerSpec) []serverEntry {
	out := make([]serverEntry, 0, len(ss))
	for _, s := range ss {
		if s == nil {
			continue
		}
		node, err := specToYAMLNode(s)
		if err != nil {
			continue
		}
		out = append(out, serverEntry{key: keyOf(s.Ip, s.Port), node: node})
	}
	return out
}

func adminEntries(as []*spec.AdminServerSpec) []serverEntry {
	out := make([]serverEntry, 0, len(as))
	for _, a := range as {
		if a == nil {
			continue
		}
		node, err := specToYAMLNode(a)
		if err != nil {
			continue
		}
		out = append(out, serverEntry{key: keyOf(a.Ip, a.Port), node: node})
	}
	return out
}

func envoyEntries(es []*spec.EnvoyServerSpec) []serverEntry {
	out := make([]serverEntry, 0, len(es))
	for _, e := range es {
		if e == nil {
			continue
		}
		node, err := specToYAMLNode(e)
		if err != nil {
			continue
		}
		// Envoy has no single service port (it terminates filer/s3/
		// webdav on different ones), so dedupe by IP. Inventory schema
		// disallows duplicate (ip, role) entries, so per-IP keying is
		// safe and matches the worker convention.
		out = append(out, serverEntry{key: e.Ip + ":envoy", node: node})
	}
	return out
}

func workerEntries(ws []*spec.WorkerServerSpec) []serverEntry {
	out := make([]serverEntry, 0, len(ws))
	for _, w := range ws {
		if w == nil {
			continue
		}
		node, err := specToYAMLNode(w)
		if err != nil {
			continue
		}
		// Worker has no service Port; use a stable sentinel so the
		// append index doesn't collapse multiple worker entries on the
		// same IP. Inventory schema disallows duplicate (ip, role)
		// entries already, so per-IP keying is safe.
		out = append(out, serverEntry{key: w.Ip + ":worker", node: node})
	}
	return out
}

// keyOf builds the ip:port lookup key. Workers use a string sentinel
// because they have no Port; everyone else has a non-zero Port at
// generation time.
func keyOf(ip string, port int) string {
	return fmt.Sprintf("%s:%d", ip, port)
}

// specToYAMLNode encodes v directly to a yaml.Node, skipping the
// Marshal→Unmarshal round-trip the previous implementation used. The
// resulting node honors the same field order, omitempty rules, and
// scalar styles yaml.Marshal would produce, so an appended entry is
// indistinguishable from one written by greenfield Marshal.
func specToYAMLNode(v interface{}) (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(v); err != nil {
		return nil, err
	}
	return &n, nil
}
