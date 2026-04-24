package cmd

import "testing"

func TestFactsFilePath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"cluster.yaml", "cluster.facts.json"},
		{"cluster.yml", "cluster.facts.json"},
		{"/etc/seaweed-up/prod.yaml", "/etc/seaweed-up/prod.facts.json"},
		{"./out/topo.yml", "./out/topo.facts.json"},
		// No recognized extension — append, don't strip.
		{"cluster", "cluster.facts.json"},
		{"plan.txt", "plan.txt.facts.json"},
	}
	for _, tc := range cases {
		if got := factsFilePath(tc.in); got != tc.want {
			t.Errorf("factsFilePath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
