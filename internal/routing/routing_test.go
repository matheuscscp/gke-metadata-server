// Copyright 2026 Matheus Pimenta.
// SPDX-License-Identifier: AGPL-3.0

package routing

import (
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/matheuscscp/gke-metadata-server/api"
	"github.com/matheuscscp/gke-metadata-server/internal/attestation"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func fakeCgroupRoot(t *testing.T) (root, pods string) {
	t.Helper()
	root = t.TempDir()
	pods = filepath.Join(root, "kubepods.slice")
	require.NoError(t, os.Mkdir(pods, 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(root, "system.slice"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cgroup.procs"), nil, 0o644))

	oldMount, oldSelf := attestation.CgroupV2Mount, selfCgroupDir
	attestation.CgroupV2Mount = root
	selfCgroupDir = func() (string, error) {
		return filepath.Join(pods, "kubepods-besteffort.slice", "emulator.scope"), nil
	}
	t.Cleanup(func() {
		attestation.CgroupV2Mount, selfCgroupDir = oldMount, oldSelf
	})
	return root, pods
}

func TestCheckCgroupPath(t *testing.T) {
	root, pods := fakeCgroupRoot(t)

	for _, tt := range []struct {
		name      string
		path      string
		want      string
		wantErr   string
		wantErrIs error
	}{
		{
			name: "empty means the root",
			want: root,
		},
		{
			name: "Pods' subtree",
			path: pods,
			want: pods,
		},
		{
			name: "path is cleaned",
			path: pods + "/",
			want: pods,
		},
		{
			name:    "relative path",
			path:    "kubepods.slice",
			wantErr: "must be a directory under",
		},
		{
			name:    "outside the root",
			path:    "/etc",
			wantErr: "must be a directory under",
		},
		{
			name:    "out of the root through ..",
			path:    root + "/../etc",
			wantErr: "must be a directory under",
		},
		{
			name:      "missing directory",
			path:      filepath.Join(root, "kubepods"),
			wantErrIs: fs.ErrNotExist,
		},
		{
			name:    "not a directory",
			path:    filepath.Join(root, "cgroup.procs"),
			wantErr: "not a directory",
		},
		{
			name:    "directory without the emulator's own Pod",
			path:    filepath.Join(root, "system.slice"),
			wantErr: "must contain the emulator's own Pod cgroup",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := checkCgroupPath(tt.path)
			if tt.wantErrIs != nil {
				assert.ErrorIs(t, err, tt.wantErrIs)
				return
			}
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestLoadAndAttach(t *testing.T) {
	fakeCgroupRoot(t)

	for _, tt := range []struct {
		name    string
		mode    string
		path    string
		wantErr string
	}{
		{
			name: "None mode ignores the cgroup path",
			mode: api.RoutingModeNone,
			path: "/etc",
		},
		{
			name:    "invalid cgroup path in eBPF mode",
			mode:    api.RoutingModeBPF,
			path:    "/etc",
			wantErr: "must be a directory under",
		},
		{
			name:    "invalid routing mode",
			mode:    "ebpf",
			wantErr: "invalid routing mode: ebpf",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{api.AnnotationRoutingMode: tt.mode},
			}}
			mode, cgroupPath, closeRoute, err := LoadAndAttach(node, netip.Addr{}, 0, tt.path)
			if tt.wantErr != "" {
				assert.ErrorContains(t, err, tt.wantErr)
				assert.Empty(t, mode)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.mode, mode)
			assert.Empty(t, cgroupPath)
			assert.NoError(t, closeRoute())
		})
	}
}
