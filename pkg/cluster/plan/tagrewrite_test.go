package plan

import (
	"strings"
	"testing"

	"github.com/seaweedfs/seaweed-up/pkg/cluster/inventory"
)

// invWithTags returns an inventory carrying the supplied tag→IP
// mappings (plus a master so Validate is happy). Each tag's host
// is `external` so the SSH-config conflict check stays out of the
// way.
func invWithTags(t *testing.T, tags map[string]string) *inventory.Inventory {
	t.Helper()
	inv := &inventory.Inventory{
		Hosts: []inventory.Host{
			{IP: "10.0.0.11", Roles: []string{"master"}},
		},
	}
	for tag, ip := range tags {
		inv.Hosts = append(inv.Hosts, inventory.Host{
			IP: ip, Roles: []string{"external"}, Tag: tag,
		})
	}
	if err := inv.Validate(); err != nil {
		t.Fatalf("inventory.Validate: %v", err)
	}
	return inv
}

func TestRewriteTagReferences_substitutesSingle(t *testing.T) {
	inv := invWithTags(t, map[string]string{"postgres-metadata": "10.0.0.41"})
	dsn := "postgres://seaweed:s3cret@tag:postgres-metadata:5432/seaweedfs?sslmode=disable"
	got, err := RewriteTagReferences(dsn, inv)
	if err != nil {
		t.Fatalf("RewriteTagReferences: %v", err)
	}
	want := "postgres://seaweed:s3cret@10.0.0.41:5432/seaweedfs?sslmode=disable"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestRewriteTagReferences_noTagPassesThrough(t *testing.T) {
	inv := invWithTags(t, nil)
	in := "postgres://user:pass@10.0.0.41:5432/db"
	got, err := RewriteTagReferences(in, inv)
	if err != nil {
		t.Fatalf("RewriteTagReferences: %v", err)
	}
	if got != in {
		t.Errorf("no-tag DSN should pass through unchanged; got %q", got)
	}
}

func TestRewriteTagReferences_multipleTags(t *testing.T) {
	inv := invWithTags(t, map[string]string{
		"primary": "10.0.0.41",
		"replica": "10.0.0.42",
	})
	dsn := "tag:primary,tag:replica?weight=1"
	got, err := RewriteTagReferences(dsn, inv)
	if err != nil {
		t.Fatalf("RewriteTagReferences: %v", err)
	}
	if !strings.Contains(got, "10.0.0.41") || !strings.Contains(got, "10.0.0.42") {
		t.Errorf("both tag substitutions missing in %q", got)
	}
	if strings.Contains(got, "tag:") {
		t.Errorf("output still contains a literal tag: reference: %q", got)
	}
}

func TestRewriteTagReferences_unknownTagErrors(t *testing.T) {
	inv := invWithTags(t, map[string]string{"primary": "10.0.0.41"})
	_, err := RewriteTagReferences("postgres://u:p@tag:nonexistent:5432/db", inv)
	if err == nil {
		t.Fatal("expected error for unknown tag, got nil")
	}
	if !strings.Contains(err.Error(), "tag:nonexistent") {
		t.Errorf("error should name the missing tag, got: %v", err)
	}
}

func TestRewriteTagReferences_ipv6BracketsTaggedHost(t *testing.T) {
	// v6 IP must be wrapped in brackets so it slots into a URL
	// authority cleanly: `@[2001:db8::1]:5432`.
	inv := invWithTags(t, map[string]string{"v6db": "2001:db8::1"})
	got, err := RewriteTagReferences("postgres://u:p@tag:v6db:5432/db", inv)
	if err != nil {
		t.Fatalf("RewriteTagReferences: %v", err)
	}
	if !strings.Contains(got, "@[2001:db8::1]:5432/") {
		t.Errorf("v6 substitution should bracket the address; got %q", got)
	}
}

func TestRewriteTagReferences_nilInventoryNoOp(t *testing.T) {
	in := "postgres://u:p@tag:foo/db"
	got, err := RewriteTagReferences(in, nil)
	if err != nil {
		t.Fatalf("nil inventory should be a no-op, got err %v", err)
	}
	if got != in {
		t.Errorf("nil inventory should pass through unchanged; got %q", got)
	}
}

func TestRewriteTagReferences_doesNotMatchEmbeddedSubstring(t *testing.T) {
	// `flagtag:foo` shouldn't trigger — the regexp boundary on the
	// left guards against a `tag:` that's actually a suffix of some
	// other identifier.
	inv := invWithTags(t, map[string]string{"foo": "10.0.0.41"})
	in := "postgres://flagtag:foo@host/db"
	got, err := RewriteTagReferences(in, inv)
	if err != nil {
		t.Fatalf("RewriteTagReferences: %v", err)
	}
	if got != in {
		t.Errorf("embedded `tag:` shouldn't be rewritten; got %q", got)
	}
}
