// Code generated by dpgen. DO NOT EDIT.

// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package config

// BPFOverlay is a configuration struct for a Cilium datapath object. Warning:
// do not instantiate directly! Always use [NewBPFOverlay] to ensure the default
// values configured in the ELF are honored.
type BPFOverlay struct {
	// MTU of the device the bpf program is attached to (default: MTU set in
	// node_config.h by agent).
	DeviceMTU uint16 `config:"device_mtu"`
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

	Node
}

func NewBPFOverlay(node Node) *BPFOverlay {
	return &BPFOverlay{0x5dc, 0x0, 0x0, 0x0, 0x0,
		[16]byte{0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0, 0x0},
		node}
}
