package storage

import "testing"

// A UserVolumeConfig formats the disk it provisions, and Rook needs that disk raw, so rook-v1 renders no
// volume document whatever the selector says.
func TestRookV1_TalosPatchesRendersNoVolume(t *testing.T) {
	for _, selector := range []string{"", `disk.transport == "nvme" && !system_disk`} {
		docs, err := rookV1{}.TalosPatches(selector)
		if err != nil {
			t.Fatalf("TalosPatches(%q) error: %v", selector, err)
		}

		if len(docs) != 0 {
			t.Errorf("TalosPatches(%q) returned %d documents, want none", selector, len(docs))
		}
	}
}

func TestRookV1_RegistersNothingWithTheNode(t *testing.T) {
	p := rookV1{}

	if got := p.RequiredExtensions(); len(got) != 0 {
		t.Errorf("RequiredExtensions() = %v, want none", got)
	}

	if err := p.CheckExtensions(nil); err != nil {
		t.Errorf("CheckExtensions(nil) = %v, want nil", err)
	}

	if got := p.KubeletMounts(); len(got) != 0 {
		t.Errorf("KubeletMounts() = %+v, want none", got)
	}

	if got := p.NodeLabels(); len(got) != 0 {
		t.Errorf("NodeLabels() = %v, want none", got)
	}

	if got := p.NodeAnnotations(); len(got) != 0 {
		t.Errorf("NodeAnnotations() = %v, want none", got)
	}
}

func TestRookV1_CheckConsistencyHasNothingToCompare(t *testing.T) {
	if err := (rookV1{}).CheckConsistency(LiveConfig{}); err != nil {
		t.Errorf("CheckConsistency(zero LiveConfig) = %v, want nil", err)
	}
}
