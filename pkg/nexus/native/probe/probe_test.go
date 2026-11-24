// SPDX-FileCopyrightText: 2026 Milos Vasic
// SPDX-License-Identifier: Apache-2.0

package probe

import (
	"context"
	"io"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"digital.vasic.containers/pkg/remote"
)

func TestProbeLocal_PopulatesHost(t *testing.T) {
	r, err := ProbeLocal(context.Background())
	require.NoError(t, err)
	require.Equal(t, runtime.GOOS, r.OS)
	require.Equal(t, runtime.GOARCH, r.Arch)
	require.Greater(t, r.CPUCores, 0)
	require.Greater(t, r.MemoryTotalMB, uint64(0))
}

type fakeRemoteExec struct {
	replies map[string]*remote.CommandResult
}

func (f *fakeRemoteExec) Execute(_ context.Context, _ remote.RemoteHost, cmd string) (*remote.CommandResult, error) {
	if r, ok := f.replies[cmd]; ok {
		return r, nil
	}
	return &remote.CommandResult{ExitCode: 127}, nil
}
func (f *fakeRemoteExec) ExecuteStream(context.Context, remote.RemoteHost, string) (io.ReadCloser, error) {
	return nil, nil
}
func (f *fakeRemoteExec) CopyFile(context.Context, remote.RemoteHost, string, string) error {
	return nil
}
func (f *fakeRemoteExec) CopyDir(context.Context, remote.RemoteHost, string, string) error {
	return nil
}
func (f *fakeRemoteExec) IsReachable(context.Context, remote.RemoteHost) bool { return true }

func TestProbeRemote_Thinker(t *testing.T) {
	exec := &fakeRemoteExec{replies: map[string]*remote.CommandResult{
		"nvidia-smi --query-gpu=index,name,driver_version,memory.total,memory.free,utilization.gpu,compute_cap --format=csv,noheader,nounits 2>/dev/null || true": {
			ExitCode: 0,
			Stdout:   "0, NVIDIA GeForce RTX 3060, 535.104.05, 6144, 5800, 3, 8.6\n",
		},
	}}
	rep, err := ProbeRemote(
		context.Background(), exec,
		remote.RemoteHost{Name: "thinker", Address: "host-a.example.com"},
	)
	require.NoError(t, err)
	require.Equal(t, "thinker", rep.Host)
	require.Len(t, rep.GPU, 1)
	require.Equal(t, "nvidia", rep.GPU[0].Vendor)
	require.Equal(t, 6144, rep.GPU[0].VRAMTotalMB)
}
