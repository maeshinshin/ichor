package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// fixtureRecord is the record of testdata/chart, which no CRD change touches. It moves only when the
// measurement does: the encoding, the pinned timestamps, status, release name, namespace or Kubernetes
// version, or a Helm or Go upgrade that changes the release JSON or the gzip output. Re-pin it then.
// The real chart is not pinned, since every CRD change would move it; the gate in main is its check.
const fixtureRecord = 704

func TestMeasureFixtureIsPinnedAndRepeatable(t *testing.T) {
	got, err := measure("testdata/chart")
	if err != nil {
		t.Fatal(err)
	}
	if got != fixtureRecord {
		t.Errorf("record of testdata/chart is %d bytes, pinned %d", got, fixtureRecord)
	}
	if again, _ := measure("testdata/chart"); again != got {
		t.Errorf("a second measurement gave %d, the first %d", again, got)
	}
}

// The size is of what Helm stores, so decode it: a record that is not deployed, not at the pinned time or
// rendered for another release would be a different figure from the one the ADRs record.
func TestRecordIsTheDeployedReleaseAtThePinnedTime(t *testing.T) {
	rel, err := record("testdata/chart")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.StdEncoding.DecodeString(rel)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	js, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Name, Namespace, Manifest string
		Info                      struct {
			FirstDeployed string `json:"first_deployed"`
			LastDeployed  string `json:"last_deployed"`
			Status        string `json:"status"`
			Description   string `json:"description"`
		}
	}
	if err := json.Unmarshal(js, &r); err != nil {
		t.Fatal(err)
	}
	if r.Name != "ichor" || r.Namespace != "ichor-system" || r.Info.Status != "deployed" || r.Info.Description != "Install complete" ||
		r.Info.FirstDeployed != "2026-01-01T00:00:00Z" || r.Info.LastDeployed != "2026-01-01T00:00:00Z" || !strings.Contains(r.Manifest, "kube: v1.36.0") {
		t.Errorf("unexpected release: %+v", r)
	}
}

func TestVerdictNamesSizeLimitAndHeadroom(t *testing.T) {
	for _, c := range []struct {
		size int
		ok   bool
		want string
	}{
		{1048576, true, "1048576 bytes of the limit of 1048576, 0 bytes of headroom"},
		{1048577, false, "1048577 bytes, over the limit of 1048576 by 1 bytes"},
		{581696, true, "581696 bytes of the limit of 1048576, 466880 bytes of headroom"},
	} {
		msg, ok := verdict(c.size)
		if ok != c.ok || !strings.Contains(msg, c.want) {
			t.Errorf("verdict(%d) = %q, %v; want %v containing %q", c.size, msg, ok, c.ok, c.want)
		}
	}
}
