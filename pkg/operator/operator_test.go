package operator

import "testing"

func TestBastionAddress(t *testing.T) {
	cases := []struct {
		name string
		in   *BastionConfig
		want string
	}{
		{"host only defaults to 22", &BastionConfig{Host: "192.71.171.132"}, "192.71.171.132:22"},
		{"explicit port field", &BastionConfig{Host: "192.71.171.132", Port: 2222}, "192.71.171.132:2222"},
		{"port carried in host wins", &BastionConfig{Host: "192.71.171.132:2200", Port: 2222}, "192.71.171.132:2200"},
		{"hostname only", &BastionConfig{Host: "bastion.example.com"}, "bastion.example.com:22"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bastionAddress(tc.in); got != tc.want {
				t.Fatalf("bastionAddress(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSetBastion(t *testing.T) {
	t.Cleanup(func() { SetBastion(nil) }) // don't leak global state into other tests

	SetBastion(&BastionConfig{Host: "192.71.171.132", User: "chris"})
	if defaultBastion == nil || defaultBastion.Host != "192.71.171.132" {
		t.Fatalf("SetBastion did not install the jump host: %+v", defaultBastion)
	}

	// An empty Host means "no bastion" — must clear, not install a broken one.
	SetBastion(&BastionConfig{Host: ""})
	if defaultBastion != nil {
		t.Fatalf("SetBastion with empty host should clear, got %+v", defaultBastion)
	}

	SetBastion(&BastionConfig{Host: "h"})
	SetBastion(nil)
	if defaultBastion != nil {
		t.Fatalf("SetBastion(nil) should clear, got %+v", defaultBastion)
	}
}
