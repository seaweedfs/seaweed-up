package spec

import "testing"

func TestVolumeServerSpec_IsRust(t *testing.T) {
	cases := map[string]bool{"": false, "go": false, "weed": false, "rust": true, "weed-volume": true}
	for engine, want := range cases {
		if got := (&VolumeServerSpec{Engine: engine}).IsRust(); got != want {
			t.Errorf("IsRust(%q) = %v, want %v", engine, got, want)
		}
	}
}

func TestSpecification_Validate_VolumeEngine(t *testing.T) {
	base := func(engine string) *Specification {
		return &Specification{
			MasterServers: []*MasterServerSpec{{Ip: "10.0.0.1"}},
			VolumeServers: []*VolumeServerSpec{{Ip: "10.0.0.1", Engine: engine}},
		}
	}
	for _, ok := range []string{"", "go", "weed", "rust", "weed-volume"} {
		if err := base(ok).Validate(); err != nil {
			t.Errorf("engine %q should be valid, got %v", ok, err)
		}
	}
	if err := base("rusty").Validate(); err == nil {
		t.Error("engine \"rusty\" should be rejected")
	}

	// A null volume_servers list item must be rejected, not panic.
	nullEntry := &Specification{
		MasterServers: []*MasterServerSpec{{Ip: "10.0.0.1"}},
		VolumeServers: []*VolumeServerSpec{nil},
	}
	if err := nullEntry.Validate(); err == nil {
		t.Error("a null volume_servers entry should be rejected")
	}
}
