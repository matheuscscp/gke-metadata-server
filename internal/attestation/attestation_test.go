// Copyright 2025 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package attestation_test

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/matheuscscp/gke-metadata-server/internal/attestation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withFakeProc redirects attestation.ProcRoot to a temporary directory for
// the duration of the test. Tests populate that directory with /<pid>/cgroup
// files mirroring the kernel's format.
func withFakeProc(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old := attestation.ProcRoot
	attestation.ProcRoot = dir
	t.Cleanup(func() { attestation.ProcRoot = old })
	return dir
}

func writePIDFile(t *testing.T, root string, pid int, name, contents string) {
	t.Helper()
	dir := filepath.Join(root, fmt.Sprintf("%d", pid))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o644))
}

const cgroupV2CgroupfsDriver = `0::/kubepods.slice/kubepods-besteffort.slice/kubepods-besteffort-pod12345678-1234-1234-1234-123456789abc.slice/cri-containerd-deadbeef.scope
`

const cgroupV2SystemdDriver = `0::/kubepods/besteffort/pod12345678-1234-1234-1234-123456789abc/abcdef
`

const cgroupV2SystemdUnderscores = `0::/kubepods.slice/kubepods-pod12345678_1234_1234_1234_123456789abc.slice/cri-containerd-deadbeef.scope
`

const cgroupNonPod = `0::/system.slice/kubelet.service
`

func TestPodUIDFromCgroup_Cgroupfs(t *testing.T) {
	root := withFakeProc(t)
	writePIDFile(t, root, 1, "cgroup", cgroupV2CgroupfsDriver)
	uid, err := attestation.PodUIDFromCgroup(1)
	require.NoError(t, err)
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", uid)
}

func TestPodUIDFromCgroup_Systemd(t *testing.T) {
	root := withFakeProc(t)
	writePIDFile(t, root, 1, "cgroup", cgroupV2SystemdDriver)
	uid, err := attestation.PodUIDFromCgroup(1)
	require.NoError(t, err)
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", uid)
}

func TestPodUIDFromCgroup_SystemdUnderscores(t *testing.T) {
	// systemd cgroup driver canonicalises hyphens in unit names to
	// underscores; the parser must normalise back to the API form.
	root := withFakeProc(t)
	writePIDFile(t, root, 1, "cgroup", cgroupV2SystemdUnderscores)
	uid, err := attestation.PodUIDFromCgroup(1)
	require.NoError(t, err)
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", uid)
}

func TestPodUIDFromCgroup_NotAPod(t *testing.T) {
	root := withFakeProc(t)
	writePIDFile(t, root, 1, "cgroup", cgroupNonPod)
	_, err := attestation.PodUIDFromCgroup(1)
	require.Error(t, err)
}

func TestPodUIDFromCgroup_Missing(t *testing.T) {
	withFakeProc(t)
	_, err := attestation.PodUIDFromCgroup(42)
	require.Error(t, err)
}

func TestPodUIDFromCgroupID(t *testing.T) {
	root := t.TempDir()
	old := attestation.CgroupV2Mount
	attestation.CgroupV2Mount = root
	t.Cleanup(func() { attestation.CgroupV2Mount = old })

	podDir := filepath.Join(root, "kubepods.slice", "kubepods-besteffort.slice",
		"kubepods-besteffort-pod12345678-1234-1234-1234-123456789abc.slice")
	require.NoError(t, os.MkdirAll(podDir, 0o755))

	var st syscall.Stat_t
	require.NoError(t, syscall.Stat(podDir, &st))

	uid, err := attestation.PodUIDFromCgroupID(st.Ino)
	require.NoError(t, err)
	assert.Equal(t, "12345678-1234-1234-1234-123456789abc", uid)
}

func TestPodUIDFromCgroupID_NotFound(t *testing.T) {
	root := t.TempDir()
	old := attestation.CgroupV2Mount
	attestation.CgroupV2Mount = root
	t.Cleanup(func() { attestation.CgroupV2Mount = old })

	_, err := attestation.PodUIDFromCgroupID(0xdeadbeef)
	require.Error(t, err)
}
