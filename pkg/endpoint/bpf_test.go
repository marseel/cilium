// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package endpoint

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/cilium/cilium/pkg/datapath/linux/config"
	datapath "github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/identity/identitymanager"
	"github.com/cilium/cilium/pkg/maps/ctmap"
	"github.com/cilium/cilium/pkg/testutils"
	testidentity "github.com/cilium/cilium/pkg/testutils/identity"
	testipcache "github.com/cilium/cilium/pkg/testutils/ipcache"
)

func TestWriteInformationalComments(t *testing.T) {
	s := setupEndpointSuite(t)

	e := NewTestEndpointWithState(s, nil, nil, s.orchestrator, nil, nil, nil, identitymanager.NewIDManager(), nil, s, testipcache.NewMockIPCache(), &FakeEndpointProxy{}, testidentity.NewMockIdentityAllocator(nil), ctmap.NewFakeGCRunner(), 100, StateWaitingForIdentity)

	var f bytes.Buffer
	err := e.writeInformationalComments(&f)
	require.NoError(t, err)
}

type writeFunc func(io.Writer) error

func BenchmarkWriteHeaderfile(b *testing.B) {
	testutils.IntegrationTest(b)

	s := setupEndpointSuite(b)

	e := NewTestEndpointWithState(s, nil, nil, s.orchestrator, nil, nil, nil, identitymanager.NewIDManager(), nil, s, testipcache.NewMockIPCache(), &FakeEndpointProxy{}, testidentity.NewMockIdentityAllocator(nil), ctmap.NewFakeGCRunner(), 100, StateWaitingForIdentity)
	configWriter := &config.HeaderfileWriter{}
	cfg := datapath.LocalNodeConfiguration{}

	targetComments := func(w io.Writer) error {
		return e.writeInformationalComments(w)
	}
	targetConfig := func(w io.Writer) error {
		return configWriter.WriteEndpointConfig(w, &cfg, e)
	}

	var buf bytes.Buffer
	file, err := os.CreateTemp("", "cilium_ep_bench_")
	if err != nil {
		b.Fatal(err)
	}
	defer file.Close()

	benchmarks := []struct {
		name   string
		output io.Writer
		write  writeFunc
	}{
		{"in-memory-info", &buf, targetComments},
		{"in-memory-cfg", &buf, targetConfig},
		{"to-disk-info", file, targetComments},
		{"to-disk-cfg", file, targetConfig},
	}

	for _, bm := range benchmarks {
		b.Run(bm.name, func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				if err := bm.write(bm.output); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
