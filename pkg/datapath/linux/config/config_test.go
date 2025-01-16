// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"testing"

	"github.com/cilium/ebpf/rlimit"
	"github.com/cilium/hive/cell"
	"github.com/cilium/hive/hivetest"
	"github.com/spf13/afero"
	"github.com/stretchr/testify/require"
	"github.com/vishvananda/netlink"

	"github.com/cilium/cilium/pkg/cidr"
	fakeTypes "github.com/cilium/cilium/pkg/datapath/fake/types"
	dpdef "github.com/cilium/cilium/pkg/datapath/linux/config/defines"
	"github.com/cilium/cilium/pkg/datapath/linux/sysctl"
	"github.com/cilium/cilium/pkg/datapath/loader"
	"github.com/cilium/cilium/pkg/datapath/tables"
	datapath "github.com/cilium/cilium/pkg/datapath/types"
	"github.com/cilium/cilium/pkg/hive"
	"github.com/cilium/cilium/pkg/maglev"
	"github.com/cilium/cilium/pkg/maps/nodemap"
	"github.com/cilium/cilium/pkg/maps/nodemap/fake"
	"github.com/cilium/cilium/pkg/option"
	"github.com/cilium/cilium/pkg/testutils"
)

var (
	dummyNodeCfg = datapath.LocalNodeConfiguration{
		NodeIPv4:           ipv4DummyAddr.AsSlice(),
		NodeIPv6:           ipv6DummyAddr.AsSlice(),
		CiliumInternalIPv4: ipv4DummyAddr.AsSlice(),
		CiliumInternalIPv6: ipv6DummyAddr.AsSlice(),
		AllocCIDRIPv4:      cidr.MustParseCIDR("10.147.0.0/16"),
		LoopbackIPv4:       ipv4DummyAddr.AsSlice(),
		Devices:            []*tables.Device{},
		NodeAddresses:      []tables.NodeAddress{},
		HostEndpointID:     1,
	}
	dummyDevCfg   = testutils.NewTestEndpoint()
	dummyEPCfg    = testutils.NewTestEndpoint()
	ipv4DummyAddr = netip.MustParseAddr("192.0.2.3")
	ipv6DummyAddr = netip.MustParseAddr("2001:db08:0bad:cafe:600d:bee2:0bad:cafe")
)

func setupConfigSuite(tb testing.TB) {
	testutils.PrivilegedTest(tb)

	tb.Helper()

	require.NoError(tb, rlimit.RemoveMemlock(), "Failed to remove memory limits")

	option.Config.EnableHostLegacyRouting = true // Disable obtaining direct routing device.
}

type badWriter struct{}

func (b *badWriter) Write(p []byte) (int, error) {
	return 0, errors.New("bad write :(")
}

type writeFn func(io.Writer, datapath.ConfigWriter) error

func writeConfig(t *testing.T, header string, write writeFn) {
	tests := []struct {
		description string
		output      io.Writer
		wantErr     bool
	}{
		{
			description: "successful write to an in-memory buffer",
			output:      &bytes.Buffer{},
			wantErr:     false,
		},
		{
			description: "write to a failing writer",
			output:      &badWriter{},
			wantErr:     true,
		},
	}
	for _, test := range tests {
		var writer datapath.ConfigWriter
		t.Logf("  Testing %s configuration: %s", header, test.description)
		h := hive.New(
			provideNodemap,
			tables.DirectRoutingDeviceCell,
			maglev.Cell,
			cell.Provide(
				fakeTypes.NewNodeAddressing,
				func() sysctl.Sysctl { return sysctl.NewDirectSysctl(afero.NewOsFs(), "/proc") },
				NewHeaderfileWriter,
			),
			cell.Invoke(func(writer_ datapath.ConfigWriter) {
				writer = writer_
			}),
		)

		tlog := hivetest.Logger(t)
		require.NoError(t, h.Start(tlog, context.TODO()))
		t.Cleanup(func() { require.NoError(t, h.Stop(tlog, context.TODO())) })
		err := write(test.output, writer)
		require.Equal(t, test.wantErr, (err != nil), "wantErr=%v, err=%s", test.wantErr, err)
	}
}

func TestWriteNodeConfig(t *testing.T) {
	setupConfigSuite(t)
	writeConfig(t, "node", func(w io.Writer, dp datapath.ConfigWriter) error {
		return dp.WriteNodeConfig(w, &dummyNodeCfg)
	})
}

func TestWriteNetdevConfig(t *testing.T) {
	writeConfig(t, "netdev", func(w io.Writer, dp datapath.ConfigWriter) error {
		return dp.WriteNetdevConfig(w, dummyDevCfg.GetOptions())
	})
}

func TestWriteStaticData(t *testing.T) {
	cfg := &HeaderfileWriter{}
	ep := &dummyEPCfg

	mapSub := loader.ELFMapSubstitutions(ep)

	var buf bytes.Buffer
	cfg.writeStaticData(&buf, ep)
	b := buf.Bytes()

	for _, v := range mapSub {
		t.Logf("Ensuring config has %s", v)
		require.True(t, bytes.Contains(b, []byte(v)))
	}
}

func createMainLink(name string, t *testing.T) *netlink.Dummy {
	link := &netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{
			Name: name,
		},
	}
	err := netlink.LinkAdd(link)
	require.NoError(t, err)

	return link
}

func createVlanLink(vlanId int, mainLink *netlink.Dummy, t *testing.T) *netlink.Vlan {
	link := &netlink.Vlan{
		LinkAttrs: netlink.LinkAttrs{
			Name:        fmt.Sprintf("%s.%d", mainLink.Name, vlanId),
			ParentIndex: mainLink.Index,
		},
		VlanProtocol: netlink.VLAN_PROTOCOL_8021Q,
		VlanId:       vlanId,
	}
	err := netlink.LinkAdd(link)
	require.NoError(t, err)

	return link
}

func TestVLANBypassConfig(t *testing.T) {
	setupConfigSuite(t)

	var devs []*tables.Device

	main1 := createMainLink("dummy0", t)
	devs = append(devs, &tables.Device{Name: main1.Name, Index: main1.Index})
	defer func() {
		netlink.LinkDel(main1)
	}()

	// Define set of vlans which we want to allow.
	allow := map[int]bool{
		4000: true,
		4001: true,
		4003: true,
	}

	for i := 4000; i < 4003; i++ {
		vlan := createVlanLink(i, main1, t)
		if allow[i] {
			devs = append(devs, &tables.Device{Index: vlan.Index, Name: vlan.Name})
		}
		defer func() {
			netlink.LinkDel(vlan)
		}()
	}

	main2 := createMainLink("dummy1", t)
	devs = append(devs, &tables.Device{Name: main2.Name, Index: main2.Index})
	defer func() {
		netlink.LinkDel(main2)
	}()

	for i := 4003; i < 4006; i++ {
		vlan := createVlanLink(i, main2, t)
		if allow[i] {
			devs = append(devs, &tables.Device{Index: vlan.Index, Name: vlan.Name})
		}
		defer func() {
			netlink.LinkDel(vlan)
		}()
	}

	option.Config.VLANBPFBypass = []int{4004}
	m, err := vlanFilterMacros(devs)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf(`switch (ifindex) { \
case %d: \
switch (vlan_id) { \
case 4000: \
case 4001: \
return true; \
} \
break; \
case %d: \
switch (vlan_id) { \
case 4003: \
case 4004: \
return true; \
} \
break; \
} \
return false;`, main1.Index, main2.Index), m)

	option.Config.VLANBPFBypass = []int{4002, 4004, 4005}
	_, err = vlanFilterMacros(devs)
	require.Error(t, err)

	option.Config.VLANBPFBypass = []int{0}
	m, err = vlanFilterMacros(devs)
	require.NoError(t, err)
	require.Equal(t, "return true", m)
}

func TestWriteNodeConfigExtraDefines(t *testing.T) {
	testutils.PrivilegedTest(t)
	setupConfigSuite(t)

	var (
		na   datapath.NodeAddressing
		magl *maglev.Maglev
	)
	h := hive.New(
		cell.Provide(
			fakeTypes.NewNodeAddressing,
		),
		maglev.Cell,
		cell.Invoke(func(
			nodeaddressing datapath.NodeAddressing,
			ml *maglev.Maglev,
		) {
			na = nodeaddressing
			magl = ml
		}),
	)

	tlog := hivetest.Logger(t)
	require.NoError(t, h.Start(tlog, context.TODO()))
	t.Cleanup(func() { h.Stop(tlog, context.TODO()) })

	var buffer bytes.Buffer

	// Assert that configurations are propagated when all generated extra defines are valid
	cfg, err := NewHeaderfileWriter(WriterParams{
		NodeAddressing:   na,
		NodeExtraDefines: nil,
		NodeExtraDefineFns: []dpdef.Fn{
			func() (dpdef.Map, error) { return dpdef.Map{"FOO": "0x1", "BAR": "0x2"}, nil },
			func() (dpdef.Map, error) { return dpdef.Map{"BAZ": "0x3"}, nil },
		},
		Sysctl:  sysctl.NewDirectSysctl(afero.NewOsFs(), "/proc"),
		NodeMap: fake.NewFakeNodeMapV2(),
		Maglev:  magl,
	})
	require.NoError(t, err)

	buffer.Reset()
	require.NoError(t, cfg.WriteNodeConfig(&buffer, &dummyNodeCfg))

	output := buffer.String()
	require.Contains(t, output, "define FOO 0x1\n")
	require.Contains(t, output, "define BAR 0x2\n")
	require.Contains(t, output, "define BAZ 0x3\n")

	// Assert that an error is returned when one extra define function returns an error
	cfg, err = NewHeaderfileWriter(WriterParams{
		NodeAddressing:   fakeTypes.NewNodeAddressing(),
		NodeExtraDefines: nil,
		NodeExtraDefineFns: []dpdef.Fn{
			func() (dpdef.Map, error) { return nil, errors.New("failing on purpose") },
		},
		Sysctl:  sysctl.NewDirectSysctl(afero.NewOsFs(), "/proc"),
		NodeMap: fake.NewFakeNodeMapV2(),
		Maglev:  magl,
	})
	require.NoError(t, err)

	buffer.Reset()
	require.Error(t, cfg.WriteNodeConfig(&buffer, &dummyNodeCfg))

	// Assert that an error is returned when one extra define would overwrite an already existing entry
	cfg, err = NewHeaderfileWriter(WriterParams{
		NodeAddressing:   fakeTypes.NewNodeAddressing(),
		NodeExtraDefines: nil,
		NodeExtraDefineFns: []dpdef.Fn{
			func() (dpdef.Map, error) { return dpdef.Map{"FOO": "0x1", "BAR": "0x2"}, nil },
			func() (dpdef.Map, error) { return dpdef.Map{"FOO": "0x3"}, nil },
		},
		Sysctl:  sysctl.NewDirectSysctl(afero.NewOsFs(), "/proc"),
		NodeMap: fake.NewFakeNodeMapV2(),
		Maglev:  magl,
	})
	require.NoError(t, err)

	buffer.Reset()
	require.Error(t, cfg.WriteNodeConfig(&buffer, &dummyNodeCfg))
}

func TestNewHeaderfileWriter(t *testing.T) {
	testutils.PrivilegedTest(t)
	setupConfigSuite(t)

	lc := hivetest.Lifecycle(t)
	magl, err := maglev.New(maglev.DefaultConfig, lc)
	require.NoError(t, err, "maglev.New")

	a := dpdef.Map{"A": "1"}
	var buffer bytes.Buffer

	_, err = NewHeaderfileWriter(WriterParams{
		NodeAddressing:     fakeTypes.NewNodeAddressing(),
		NodeExtraDefines:   []dpdef.Map{a, a},
		NodeExtraDefineFns: nil,
		Sysctl:             sysctl.NewDirectSysctl(afero.NewOsFs(), "/proc"),
		NodeMap:            fake.NewFakeNodeMapV2(),
		Maglev:             magl,
	})

	require.Error(t, err, "duplicate keys should be rejected")

	cfg, err := NewHeaderfileWriter(WriterParams{
		NodeAddressing:     fakeTypes.NewNodeAddressing(),
		NodeExtraDefines:   []dpdef.Map{a},
		NodeExtraDefineFns: nil,
		Sysctl:             sysctl.NewDirectSysctl(afero.NewOsFs(), "/proc"),
		NodeMap:            fake.NewFakeNodeMapV2(),
		Maglev:             magl,
	})
	require.NoError(t, err)
	require.NoError(t, cfg.WriteNodeConfig(&buffer, &dummyNodeCfg))
	require.Contains(t, buffer.String(), "define A 1\n")
}

var provideNodemap = cell.Provide(func() nodemap.MapV2 {
	return fake.NewFakeNodeMapV2()
})
