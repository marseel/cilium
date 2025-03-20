// Code generated by dpgen. DO NOT EDIT.

// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package config

// BPFHost is a configuration struct for a Cilium datapath object. Warning: do
// not instantiate directly! Always use [NewBPFHost] to ensure the default
// values configured in the ELF are honored.
type BPFHost struct {
	// MTU of the device the bpf program is attached to (default: MTU set in
	// node_config.h by agent).
	DeviceMTU uint16 `config:"device_mtu"`
	// Length of the Ethernet header on this device. May be set to zero on L2-less
	// devices. (default __ETH_HLEN).
	EthHeaderLength uint8 `config:"eth_header_length"`
	// Pull security context from IP cache.
	HostSecctxFromIPCache uint32 `config:"host_secctx_from_ipcache"`
	// Ifindex of the interface the bpf program is attached to.
	InterfaceIfindex uint32 `config:"interface_ifindex"`
	// First 32 bits of the MAC address of the interface the bpf program is
	// attached to.
	InterfaceMAC1 uint32 `config:"interface_mac_1"`
	// Latter 16 bits of the MAC address of the interface the bpf program is
	// attached to.
	InterfaceMAC2 uint16 `config:"interface_mac_2"`
	// Masquerade address for IPv4 traffic.
	NATIPv4Masquerade uint32 `config:"nat_ipv4_masquerade"`
	// Masquerade address for IPv6 traffic.
	NATIPv6Masquerade [16]byte `config:"nat_ipv6_masquerade"`
	// The endpoint's security label.
	SecurityLabel uint32 `config:"security_label"`

	Node
}

func NewBPFHost(node Node) *BPFHost {
	return &BPFHost{0x5dc, 0xe, 0x0, 0x0, 0x0, 0x0, 0x0,
		[16]byte{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
		0x0, node}
}
