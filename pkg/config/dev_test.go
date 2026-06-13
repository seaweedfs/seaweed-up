package config

import "testing"

func TestPickDevAsset(t *testing.T) {
	assets := []Asset{
		{Name: "weed-large-disk-20260607-1918-linux-amd64.tar.gz"},
		{Name: "weed-large-disk-20260607-1918-linux-amd64.tar.gz.md5"},
		{Name: "weed-large-disk-20260606-1200-linux-amd64.tar.gz"},        // older
		{Name: "weed-large-disk-20260607-1918-linux-arm64.tar.gz"},        // wrong arch
		{Name: "weed-20260607-1918-linux-amd64.tar.gz"},                   // regular variant
		{Name: "weed-large-disk-20260607-1922-windows-amd64.zip"},         // wrong os
		{Name: "weed-volume-large-disk-20260607-1925-linux-amd64.tar.gz"}, // rust large-disk
		{Name: "weed-volume-20260607-1925-linux-amd64.tar.gz"},            // rust regular
	}

	t.Run("large-disk picks newest amd64", func(t *testing.T) {
		name, id, ok := pickDevAsset(assets, "weed", true, "amd64")
		if !ok || id != "20260607-1918" || name != "weed-large-disk-20260607-1918-linux-amd64.tar.gz" {
			t.Fatalf("got name=%q id=%q ok=%v", name, id, ok)
		}
	})

	t.Run("regular variant excludes large-disk and weed-volume", func(t *testing.T) {
		name, id, ok := pickDevAsset(assets, "weed", false, "amd64")
		if !ok || name != "weed-20260607-1918-linux-amd64.tar.gz" || id != "20260607-1918" {
			t.Fatalf("got name=%q id=%q ok=%v", name, id, ok)
		}
	})

	t.Run("rust weed-volume large-disk", func(t *testing.T) {
		name, id, ok := pickDevAsset(assets, "weed-volume", true, "amd64")
		if !ok || id != "20260607-1925" || name != "weed-volume-large-disk-20260607-1925-linux-amd64.tar.gz" {
			t.Fatalf("got name=%q id=%q ok=%v", name, id, ok)
		}
	})

	t.Run("rust weed-volume regular excludes large-disk", func(t *testing.T) {
		name, _, ok := pickDevAsset(assets, "weed-volume", false, "amd64")
		if !ok || name != "weed-volume-20260607-1925-linux-amd64.tar.gz" {
			t.Fatalf("got name=%q ok=%v", name, ok)
		}
	})

	t.Run("arch respected", func(t *testing.T) {
		name, _, ok := pickDevAsset(assets, "weed", true, "arm64")
		if !ok || name != "weed-large-disk-20260607-1918-linux-arm64.tar.gz" {
			t.Fatalf("got name=%q ok=%v", name, ok)
		}
	})

	t.Run("none matches", func(t *testing.T) {
		if _, _, ok := pickDevAsset(assets, "weed", true, "ppc64le"); ok {
			t.Fatalf("expected no match for ppc64le")
		}
	})

	t.Run("newest wins regardless of order", func(t *testing.T) {
		shuffled := []Asset{
			{Name: "weed-large-disk-20260101-0000-linux-amd64.tar.gz"},
			{Name: "weed-large-disk-20271231-2359-linux-amd64.tar.gz"},
			{Name: "weed-large-disk-20260607-1918-linux-amd64.tar.gz"},
		}
		_, id, ok := pickDevAsset(shuffled, "weed", true, "amd64")
		if !ok || id != "20271231-2359" {
			t.Fatalf("got id=%q ok=%v", id, ok)
		}
	})
}
