package plan

import (
	"fmt"
	"regexp"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
)

// tagRefRE matches `tag:<name>` symbolic references in a DSN. Tag
// names follow the same lenient grammar as DNS labels — letters,
// digits, dot, dash, underscore — so postgres-metadata, redis_main,
// metadata.staging, etc. all work. Anchored with word boundaries on
// the left so we don't match `flagtag:foo` or `???tag:`.
//
// Why a regexp instead of a hand-rolled scanner: a DSN like
// `postgres://user:pw@tag:metadata-db:5432/db?sslmode=disable`
// contains other `:` separators around the tag substring, and we
// want to substitute exactly the `tag:metadata-db` segment without
// touching `user:pw` or `:5432/db`. The (^|\W) prefix guards the
// left edge; the [a-zA-Z0-9_.-]+ on the right consumes only the
// tag name and stops at the next `:` or `/` etc.
var tagRefRE = regexp.MustCompile(`(^|\W)tag:([a-zA-Z0-9_.\-]+)`)

// RewriteTagReferences resolves `tag:<name>` references in dsn by
// substituting the tagged host's IP from inv. Returns the rewritten
// string and a non-nil error when any referenced tag isn't declared
// on a host (typo, host removed, etc.) — failing loud rather than
// passing through a `tag:` literal that ParseFilerBackendDSN would
// then reject as a malformed host.
//
// When dsn carries no tag references the function is a fast no-op
// and returns the input unchanged. Useful for the operator who just
// wants to keep typing literal IPs/hostnames.
//
// IPv6: a v6 IP is wrapped in brackets so it slots cleanly into a
// URL authority (`postgres://user:pw@[2001:db8::1]:5432/db`). v4
// IPs and DNS names are substituted verbatim.
func RewriteTagReferences(dsn string, inv *inventory.Inventory) (string, error) {
	if inv == nil || dsn == "" {
		return dsn, nil
	}
	// Cheap pre-check: avoid the regexp engine on the common case
	// where the operator typed a plain DSN with no tag: forms.
	matches := tagRefRE.FindAllStringSubmatchIndex(dsn, -1)
	if len(matches) == 0 {
		return dsn, nil
	}

	// Build the output by interleaving unchanged spans with
	// resolved IPs. ReplaceAllStringFunc would work but loses the
	// per-match error path we want for unknown tags.
	var out []byte
	last := 0
	for _, m := range matches {
		// m = [start, end, prefixStart, prefixEnd, tagStart, tagEnd]
		prefixStart, prefixEnd := m[2], m[3]
		tagStart, tagEnd := m[4], m[5]
		tag := dsn[tagStart:tagEnd]

		host, ok := inv.HostByTag(tag)
		if !ok {
			// Stay generic here so the error reads sensibly when
			// future callers reuse RewriteTagReferences for more
			// than just --filer-backend; the cmd layer already
			// wraps with "filer backend: " for the operator-facing
			// message.
			return "", fmt.Errorf("tag:%s not found in inventory", tag)
		}

		out = append(out, dsn[last:prefixEnd]...) // includes the prefix byte
		out = append(out, formatTagSubstitution(host.IP)...)
		last = tagEnd
		_ = prefixStart // already covered by the append above
	}
	out = append(out, dsn[last:]...)
	return string(out), nil
}

// formatTagSubstitution renders a host IP for inline DSN
// substitution. v6 IPs get bracketed so the surrounding URL parser
// sees a single authority component; v4 and DNS names go in raw.
// Detection is shallow on purpose — anything containing `:` is
// treated as v6.
func formatTagSubstitution(ip string) string {
	for i := 0; i < len(ip); i++ {
		if ip[i] == ':' {
			return "[" + ip + "]"
		}
	}
	return ip
}
