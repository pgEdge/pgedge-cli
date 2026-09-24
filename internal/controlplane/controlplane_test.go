package controlplane

import "testing"

func TestDescribe(t *testing.T) {
	info := (&Module{}).Describe()
	if info.Name != "controlplane" || info.Short == "" {
		t.Errorf("unexpected: %+v", info)
	}
	if info.Version != Version {
		t.Errorf("Version = %q, want %q", info.Version, Version)
	}
}
