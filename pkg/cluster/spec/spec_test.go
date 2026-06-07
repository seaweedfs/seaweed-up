package spec

import "testing"

func masterOnly() []*MasterServerSpec {
	return []*MasterServerSpec{{Ip: "10.0.0.1", Port: 9333}}
}

func TestValidate_Bastion(t *testing.T) {
	cases := []struct {
		name    string
		bastion *BastionSpec
		wantErr bool
	}{
		{"no bastion", nil, false},
		{"valid host+port", &BastionSpec{Host: "192.71.171.132", Port: 22}, false},
		{"port unset is allowed (defaults to 22)", &BastionSpec{Host: "bastion"}, false},
		{"blank host", &BastionSpec{Host: "   "}, true},
		{"empty host", &BastionSpec{Host: "", Port: 22}, true},
		{"negative port", &BastionSpec{Host: "bastion", Port: -1}, true},
		{"port too large", &BastionSpec{Host: "bastion", Port: 70000}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Specification{MasterServers: masterOnly()}
			s.GlobalOptions.Bastion = tc.bastion
			err := s.Validate()
			if tc.wantErr && err == nil {
				t.Fatalf("expected validation error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
		})
	}
}
