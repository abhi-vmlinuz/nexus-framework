package k8s

import "testing"

func TestNormalizeRole(t *testing.T) {
	cases := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{"", RoleChallenge, false},
		{RoleChallenge, RoleChallenge, false},
		{RoleEnv, RoleEnv, false},
		{"env ", "", true},
		{"Env", "", true},
		{"target", "", true},
	}
	for _, tc := range cases {
		got, err := NormalizeRole(tc.in)
		if tc.wantErr {
			if err == nil {
				t.Errorf("NormalizeRole(%q): want error, got %q", tc.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("NormalizeRole(%q): unexpected error %v", tc.in, err)
			continue
		}
		if got != tc.want {
			t.Errorf("NormalizeRole(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestBuildPodVolumesReadOnlyByDefault(t *testing.T) {
	vols, mounts, err := buildPodVolumes([]VolumeMount{
		{HostPath: "/var/lib/nexus/artifacts/abc", MountPath: "/artifacts"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(vols) != 1 || len(mounts) != 1 {
		t.Fatalf("want 1 volume and 1 mount, got %d and %d", len(vols), len(mounts))
	}
	if mounts[0].ReadOnly != true {
		t.Error("zero-value ReadWrite must produce a read-only mount")
	}
	if mounts[0].MountPath != "/artifacts" {
		t.Errorf("mount path = %q", mounts[0].MountPath)
	}
	if vols[0].Name != mounts[0].Name {
		t.Error("volume and mount names must match")
	}
	if vols[0].HostPath == nil || vols[0].HostPath.Path != "/var/lib/nexus/artifacts/abc" {
		t.Error("host path not wired through")
	}
}

func TestBuildPodVolumesReadWrite(t *testing.T) {
	_, mounts, err := buildPodVolumes([]VolumeMount{
		{HostPath: "/tmp/scratch", MountPath: "/scratch", ReadWrite: true},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mounts[0].ReadOnly {
		t.Error("ReadWrite=true must produce a writable mount")
	}
}

func TestBuildPodVolumesRejectsBadInput(t *testing.T) {
	cases := map[string]VolumeMount{
		"missing host path":   {MountPath: "/artifacts"},
		"missing mount path":  {HostPath: "/var/lib/nexus/artifacts"},
		"relative host path":  {HostPath: "artifacts", MountPath: "/artifacts"},
		"relative mount path": {HostPath: "/var/lib/nexus/artifacts", MountPath: "artifacts"},
	}
	for name, m := range cases {
		if _, _, err := buildPodVolumes([]VolumeMount{m}); err == nil {
			t.Errorf("%s: want error, got none", name)
		}
	}
}

func TestBuildPodVolumesEmpty(t *testing.T) {
	vols, mounts, err := buildPodVolumes(nil)
	if err != nil || vols != nil || mounts != nil {
		t.Errorf("empty input: got %v %v %v", vols, mounts, err)
	}
}
