package plan

import (
	"fmt"
	"regexp"
	"strings"

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

// RewriteTagReferences resolves `tag:<name>` references in dsn's
// authority host segment (the bytes after `://[userinfo@]` and
// before the next `/?#`) by substituting the tagged host's IP from
// inv. Other parts of the DSN — scheme, userinfo, port, path,
// query, fragment — are left strictly alone. Constraining the
// rewrite to the host position avoids clobbering literal `tag:`
// substrings that legitimately appear in passwords (`user:tag:secret@…`)
// or query values (`?note=tag:prod`).
//
// Returns a non-nil error when a tag in the host segment isn't
// declared on any host in the inventory (typo, host removed, etc.)
// — failing loud rather than passing through a `tag:` literal that
// ParseFilerBackendDSN would then reject as a malformed host.
//
// Fast no-ops, in priority order:
//   - inv is nil or dsn is empty
//   - dsn doesn't contain the substring "tag:" anywhere
//   - dsn doesn't contain the scheme separator "://" (we can't
//     locate the host segment without one; the operator gets the
//     literal pass-through and ParseFilerBackendDSN can complain
//     about the missing scheme on its own terms)
//
// IPv6: a v6 IP is wrapped in brackets so it slots cleanly into a
// URL authority (`postgres://user:pw@[2001:db8::1]:5432/db`). v4
// IPs and DNS names are substituted verbatim.
func RewriteTagReferences(dsn string, inv *inventory.Inventory) (string, error) {
	if inv == nil || dsn == "" {
		return dsn, nil
	}
	// Cheap pre-check: avoid spinning up the regexp state machine
	// on the common case where the operator typed a plain DSN with
	// no tag: forms. strings.Contains is O(n) byte-by-byte; the
	// regex would do considerably more work to reach the same "no
	// matches" answer.
	if !strings.Contains(dsn, "tag:") {
		return dsn, nil
	}

	// Locate the authority host segment — the slice we're allowed
	// to touch. Anything outside it (userinfo password, path,
	// query, fragment) stays verbatim even if it contains `tag:`.
	hostStart, hostEnd, ok := authorityHostRange(dsn)
	if !ok {
		return dsn, nil
	}
	rewritten, err := rewriteTagsInRange(dsn, hostStart, hostEnd, inv)
	if err != nil {
		return "", err
	}
	return dsn[:hostStart] + rewritten + dsn[hostEnd:], nil
}

// authorityHostRange returns the [start, end) byte offsets of the
// host segment within a URL-style DSN: everything after `://` and
// the optional `userinfo@`, up to the first authority terminator
// (`/`, `?`, `#`) or end-of-string. The bool reports whether a
// scheme separator was found at all; callers use it to skip the
// rewrite on non-URL inputs.
//
// Splitting the userinfo on the LAST `@` handles passwords that
// contain `@` (e.g. `user:p@ss@host`); the URL spec is ambiguous
// here but the de-facto convention is "everything before the last
// @ is userinfo". Authority-end uses the FIRST occurrence of `/`,
// `?`, or `#` after the scheme — a literal `?` inside userinfo
// would already have made the DSN unparseable.
func authorityHostRange(dsn string) (start, end int, ok bool) {
	scheme := strings.Index(dsn, "://")
	if scheme < 0 {
		return 0, 0, false
	}
	authStart := scheme + 3
	authEnd := len(dsn)
	if i := strings.IndexAny(dsn[authStart:], "/?#"); i >= 0 {
		authEnd = authStart + i
	}
	hostStart := authStart
	if at := strings.LastIndex(dsn[authStart:authEnd], "@"); at >= 0 {
		hostStart = authStart + at + 1
	}
	return hostStart, authEnd, true
}

// rewriteTagsInRange runs the tag regex against dsn[start:end] and
// returns the substituted segment. Errors loudly on unknown tags;
// no-op when the segment carries no tag references.
func rewriteTagsInRange(dsn string, start, end int, inv *inventory.Inventory) (string, error) {
	segment := dsn[start:end]
	matches := tagRefRE.FindAllStringSubmatchIndex(segment, -1)
	if len(matches) == 0 {
		return segment, nil
	}
	// Pre-allocate at the segment length: the common substitution
	// (tag:<short-name> → 10.0.0.X) is roughly the same number of
	// bytes, so this gets us through most rewrites in a single
	// allocation. v6 substitutions slightly grow the output and
	// trigger one append-resize, which is fine.
	out := make([]byte, 0, len(segment))
	last := 0
	for _, m := range matches {
		// m = [start, end, prefixStart, prefixEnd, tagStart, tagEnd].
		// prefixStart isn't needed: segment[last:prefixEnd] copies
		// everything from the previous match's tail through and
		// including the boundary character (the `\W` or BOL the
		// regex matched), so the substitution drops in cleanly.
		_, prefixEnd := m[2], m[3]
		tagStart, tagEnd := m[4], m[5]
		tag := segment[tagStart:tagEnd]

		host, ok := inv.HostByTag(tag)
		if !ok {
			// Stay generic here so the error reads sensibly when
			// future callers reuse RewriteTagReferences for more
			// than just --filer-backend; the cmd layer already
			// wraps with "filer backend: " for the operator-facing
			// message.
			return "", fmt.Errorf("tag:%s not found in inventory", tag)
		}
		out = append(out, segment[last:prefixEnd]...)
		out = append(out, formatTagSubstitution(host.IP)...)
		last = tagEnd
	}
	out = append(out, segment[last:]...)
	return string(out), nil
}

// formatTagSubstitution renders a host IP for inline DSN
// substitution. v6 IPs get bracketed so the surrounding URL parser
// sees a single authority component; v4 and DNS names go in raw.
// Detection is shallow on purpose — anything containing `:` is
// treated as v6.
//
// Already-bracketed inputs (operator typed `ip: "[2001:db8::1]"`
// in inventory.yaml — natural after a copy-paste from a connection
// URL) pass through unchanged so the rewrite doesn't produce
// `@[[2001:db8::1]]:5432`, which the DSN parser would reject with
// a confusing host-shaped error.
func formatTagSubstitution(ip string) string {
	if strings.HasPrefix(ip, "[") {
		return ip
	}
	if strings.Contains(ip, ":") {
		return "[" + ip + "]"
	}
	return ip
}
