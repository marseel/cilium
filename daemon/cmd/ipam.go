// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package cmd

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/cilium/statedb"

	"github.com/cilium/cilium/api/v1/models"
	"github.com/cilium/cilium/pkg/cidr"
	"github.com/cilium/cilium/pkg/controller"
	linuxrouting "github.com/cilium/cilium/pkg/datapath/linux/routing"
	"github.com/cilium/cilium/pkg/datapath/tables"
	"github.com/cilium/cilium/pkg/datapath/types"
	iputil "github.com/cilium/cilium/pkg/ip"
	"github.com/cilium/cilium/pkg/ipam"
	ipamOption "github.com/cilium/cilium/pkg/ipam/option"
	"github.com/cilium/cilium/pkg/logging/logfields"
	"github.com/cilium/cilium/pkg/node"
	nodeTypes "github.com/cilium/cilium/pkg/node/types"
	"github.com/cilium/cilium/pkg/option"
)

const (
	mismatchRouterIPsMsg = "Mismatch of router IPs found during restoration. The Kubernetes resource contained %s, while the filesystem contained %s. Using the router IP from the filesystem. To change the router IP, specify --%s and/or --%s."
)

// DumpIPAM dumps in the form of a map, the list of
// reserved IPv4 and IPv6 addresses.
func (d *Daemon) DumpIPAM() *models.IPAMStatus {
	allocv4, allocv6, st := d.ipam.Dump()
	status := &models.IPAMStatus{
		Status: st,
	}

	v4 := make([]string, 0, len(allocv4))
	for ip := range allocv4 {
		v4 = append(v4, ip)
	}

	v6 := make([]string, 0, len(allocv6))
	if allocv4 == nil {
		allocv4 = map[string]string{}
	}
	for ip, owner := range allocv6 {
		v6 = append(v6, ip)
		// merge allocv6 into allocv4
		allocv4[ip] = owner
	}

	if option.Config.EnableIPv4 {
		status.IPV4 = v4
	}

	if option.Config.EnableIPv6 {
		status.IPV6 = v6
	}

	status.Allocations = allocv4

	return status
}

func (d *Daemon) allocateRouterIPv4(family types.NodeAddressingFamily, fromK8s, fromFS net.IP) (net.IP, error) {
	if option.Config.LocalRouterIPv4 != "" {
		routerIP := net.ParseIP(option.Config.LocalRouterIPv4)
		if routerIP == nil {
			return nil, fmt.Errorf("Invalid local-router-ip: %s", option.Config.LocalRouterIPv4)
		}
		if d.nodeAddressing.IPv4().AllocationCIDR().Contains(routerIP) {
			log.Warn("Specified router IP is within IPv4 podCIDR.")
		}
		return routerIP, nil
	} else {
		return d.allocateDatapathIPs(family, fromK8s, fromFS)
	}
}

func (d *Daemon) allocateRouterIPv6(family types.NodeAddressingFamily, fromK8s, fromFS net.IP) (net.IP, error) {
	if option.Config.LocalRouterIPv6 != "" {
		routerIP := net.ParseIP(option.Config.LocalRouterIPv6)
		if routerIP == nil {
			return nil, fmt.Errorf("Invalid local-router-ip: %s", option.Config.LocalRouterIPv6)
		}
		if d.nodeAddressing.IPv6().AllocationCIDR().Contains(routerIP) {
			log.Warn("Specified router IP is within IPv6 podCIDR.")
		}
		return routerIP, nil
	} else {
		return d.allocateDatapathIPs(family, fromK8s, fromFS)
	}
}

// Coalesce CIDRS when allocating the DatapathIPs and healthIPs. GH #18868
func coalesceCIDRs(rCIDRs []string) (result []string, err error) {
	cidrs := make([]*net.IPNet, 0, len(rCIDRs))
	for _, k := range rCIDRs {
		ip, mask, err := net.ParseCIDR(k)
		if err != nil {
			return nil, err
		}
		cidrs = append(cidrs, &net.IPNet{IP: ip, Mask: mask.Mask})
	}
	ipv4cidr, ipv6cidr := iputil.CoalesceCIDRs(cidrs)
	combinedcidrs := append(ipv4cidr, ipv6cidr...)
	result = make([]string, len(combinedcidrs))
	for i, k := range combinedcidrs {
		result[i] = k.String()
	}
	return
}

type ipamAllocateIP interface {
	AllocateIPWithoutSyncUpstream(ip net.IP, owner string, pool ipam.Pool) (*ipam.AllocationResult, error)
}

// reallocateDatapathIPs attempts to reallocate the old router IP from IPAM.
// It prefers fromFS over fromK8s. If neither IPs can be re-allocated, log
// messages are emitted and the function returns nil.
func reallocateDatapathIPs(alloc ipamAllocateIP, fromK8s, fromFS net.IP) (result *ipam.AllocationResult) {
	if fromK8s == nil && fromFS == nil {
		// We do nothing in this case because there are no router IPs to restore.
		return nil
	}

	// If we have both an IP from the filesystem and an IP from the Kubernetes
	// resource, and they are not equal, emit a warning.
	if fromK8s != nil && fromFS != nil && !fromK8s.Equal(fromFS) {
		log.Warnf(
			mismatchRouterIPsMsg,
			fromK8s, fromFS, option.LocalRouterIPv4, option.LocalRouterIPv6,
		)
		// Above is just a warning; we still want to set the router IP regardless.
	}

	// Router IPs from the filesystem are preferred over the IPs found
	// in the Kubernetes resource (Node or CiliumNode), because we consider the
	// filesystem to be the most up-to-date source of truth.
	var err error
	if fromFS != nil {
		result, err = alloc.AllocateIPWithoutSyncUpstream(fromFS, "router", ipam.PoolDefault())
		if err != nil {
			log.WithError(err).
				WithField(logfields.IPAddr, fromFS).
				Warnf("Unable to restore router IP from filesystem")
			result = nil
		}
		// Fall back to using the IP from the Kubernetes resource if available
	}

	// If we were not able to restore the IP from the filesystem, try to use
	// the IP from the Kubernetes resource.
	if result == nil && fromK8s != nil {
		result, err = alloc.AllocateIPWithoutSyncUpstream(fromK8s, "router", ipam.PoolDefault())
		if err != nil {
			log.WithError(err).
				WithField(logfields.IPAddr, fromFS).
				Warnf("Unable to restore router IP from kubernetes")
			result = nil
		}
		// Fall back to allocating a fresh IP
	}

	if result == nil {
		log.Warn("Router IP could not be re-allocated. Need to re-allocate. This will cause brief network disruption")
	}

	return result
}

func (d *Daemon) allocateDatapathIPs(family types.NodeAddressingFamily, fromK8s, fromFS net.IP) (routerIP net.IP, err error) {
	// Avoid allocating external IP
	d.ipam.ExcludeIP(family.PrimaryExternal(), "node-ip", ipam.PoolDefault())

	// (Re-)allocate the router IP. If not possible, allocate a fresh IP.
	// In that case, the old router IP needs to be removed from cilium_host
	// by the caller.
	// This will also cause disruption of networking until all endpoints
	// have been regenerated.
	result := reallocateDatapathIPs(d.ipam, fromK8s, fromFS)
	if result == nil {
		family := ipam.DeriveFamily(family.PrimaryExternal())
		result, err = d.ipam.AllocateNextFamilyWithoutSyncUpstream(family, "router", ipam.PoolDefault())
		if err != nil {
			return nil, fmt.Errorf("Unable to allocate router IP for family %s: %w", family, err)
		}
	}

	// Coalescing multiple CIDRs. GH #18868
	if option.Config.EnableIPv4Masquerade &&
		option.Config.IPAM == ipamOption.IPAMENI &&
		result != nil &&
		len(result.CIDRs) > 0 {
		result.CIDRs, err = coalesceCIDRs(result.CIDRs)
		if err != nil {
			return nil, fmt.Errorf("failed to coalesce CIDRs: %w", err)
		}
	}

	if (option.Config.IPAM == ipamOption.IPAMENI ||
		option.Config.IPAM == ipamOption.IPAMAlibabaCloud ||
		option.Config.IPAM == ipamOption.IPAMAzure) && result != nil {
		var routingInfo *linuxrouting.RoutingInfo
		routingInfo, err = linuxrouting.NewRoutingInfo(result.GatewayIP, result.CIDRs,
			result.PrimaryMAC, result.InterfaceNumber, option.Config.IPAM,
			option.Config.EnableIPv4Masquerade)
		if err != nil {
			return nil, fmt.Errorf("failed to create router info: %w", err)
		}
		if err = routingInfo.Configure(
			result.IP,
			d.mtuConfig.GetDeviceMTU(),
			option.Config.EgressMultiHomeIPRuleCompat,
			true,
		); err != nil {
			return nil, fmt.Errorf("failed to configure router IP rules and routes: %w", err)
		}

		node.SetRouterInfo(routingInfo)

		d.controllers.CreateController("egress-route-reconciler", controller.ControllerParams{
			DoFunc: func(_ context.Context) error {
				return routingInfo.InstallRoutes(
					d.mtuConfig.GetDeviceMTU(),
					option.Config.EgressMultiHomeIPRuleCompat,
				)
			},
			// This time is a tradeoff between recovering fast from a failure and not
			// overwhelming the system with too many reconciliations.
			// Being without connectivity for at most 30 seconds and on average 15
			// seconds after a hopefully uncommon event seems like a reasonable value.
			RunInterval: 30 * time.Second,
			Context:     d.ctx,
		})
	}

	return result.IP, nil
}

func (d *Daemon) allocateHealthIPs() error {
	bootstrapStats.healthCheck.Start()
	defer bootstrapStats.healthCheck.End(true)
	if !option.Config.EnableHealthChecking || !option.Config.EnableEndpointHealthChecking {
		return nil
	}
	var healthIPv4, healthIPv6 net.IP
	if option.Config.EnableIPv4 {
		var result *ipam.AllocationResult
		var err error
		healthIPv4 = node.GetEndpointHealthIPv4()
		if healthIPv4 != nil {
			result, err = d.ipam.AllocateIPWithoutSyncUpstream(healthIPv4, "health", ipam.PoolDefault())
			if err != nil {
				log.WithError(err).WithField(logfields.IPv4, healthIPv4).
					Warn("unable to re-allocate health IPv4, a new health IPv4 will be allocated")
				healthIPv4 = nil
			}
		}
		if healthIPv4 == nil {
			result, err = d.ipam.AllocateNextFamilyWithoutSyncUpstream(ipam.IPv4, "health", ipam.PoolDefault())
			if err != nil {
				return fmt.Errorf("unable to allocate health IPv4: %w, see https://cilium.link/ipam-range-full", err)
			}
			node.SetEndpointHealthIPv4(result.IP)
		}

		// Coalescing multiple CIDRs. GH #18868
		if option.Config.EnableIPv4Masquerade &&
			option.Config.IPAM == ipamOption.IPAMENI &&
			result != nil &&
			len(result.CIDRs) > 0 {
			result.CIDRs, err = coalesceCIDRs(result.CIDRs)
			if err != nil {
				return fmt.Errorf("failed to coalesce CIDRs: %w", err)
			}
		}

		log.Debugf("IPv4 health endpoint address: %s", result.IP)

		// In ENI and AlibabaCloud ENI mode, we require the gateway, CIDRs, and the ENI MAC addr
		// in order to set up rules and routes on the local node to direct
		// endpoint traffic out of the ENIs.
		if option.Config.IPAM == ipamOption.IPAMENI || option.Config.IPAM == ipamOption.IPAMAlibabaCloud {
			if d.healthEndpointRouting, err = parseRoutingInfo(result); err != nil {
				log.WithError(err).Warn("Unable to allocate health information for ENI")
			}
		}
	}

	if option.Config.EnableIPv6 {
		var result *ipam.AllocationResult
		var err error
		healthIPv6 = node.GetEndpointHealthIPv6()
		if healthIPv6 != nil {
			result, err = d.ipam.AllocateIPWithoutSyncUpstream(healthIPv6, "health", ipam.PoolDefault())
			if err != nil {
				log.WithError(err).WithField(logfields.IPv6, healthIPv6).
					Warn("unable to re-allocate health IPv6, a new health IPv6 will be allocated")
				healthIPv6 = nil
			}
		}
		if healthIPv6 == nil {
			result, err = d.ipam.AllocateNextFamilyWithoutSyncUpstream(ipam.IPv6, "health", ipam.PoolDefault())
			if err != nil {
				if healthIPv4 != nil {
					d.ipam.ReleaseIP(healthIPv4, ipam.PoolDefault())
					node.SetEndpointHealthIPv4(nil)
				}
				return fmt.Errorf("unable to allocate health IPv6: %w, see https://cilium.link/ipam-range-full", err)
			}
			node.SetEndpointHealthIPv6(result.IP)
		}

		log.Debugf("IPv6 health endpoint address: %s", result.IP)
	}
	return nil
}

func (d *Daemon) allocateIngressIPs() error {
	bootstrapStats.ingressIPAM.Start()
	if option.Config.EnableEnvoyConfig {
		if option.Config.EnableIPv4 {
			var result *ipam.AllocationResult
			var err error

			// Reallocate the same address as before, if possible
			ingressIPv4 := node.GetIngressIPv4()
			if ingressIPv4 != nil {
				result, err = d.ipam.AllocateIPWithoutSyncUpstream(ingressIPv4, "ingress", ipam.PoolDefault())
				if err != nil {
					log.WithError(err).WithField(logfields.SourceIP, ingressIPv4).Warn("unable to re-allocate ingress IPv4.")
					result = nil
				}
			}

			// Allocate a fresh IP if not restored, or the reallocation of the restored
			// IP failed
			if result == nil {
				result, err = d.ipam.AllocateNextFamilyWithoutSyncUpstream(ipam.IPv4, "ingress", ipam.PoolDefault())
				if err != nil {
					return fmt.Errorf("unable to allocate ingress IPs: %w, see https://cilium.link/ipam-range-full", err)
				}
			}

			// Coalescing multiple CIDRs. GH #18868
			if option.Config.EnableIPv4Masquerade &&
				option.Config.IPAM == ipamOption.IPAMENI &&
				result != nil &&
				len(result.CIDRs) > 0 {
				result.CIDRs, err = coalesceCIDRs(result.CIDRs)
				if err != nil {
					return fmt.Errorf("failed to coalesce CIDRs: %w", err)
				}
			}

			node.SetIngressIPv4(result.IP)
			log.Infof("  Ingress IPv4: %s", node.GetIngressIPv4())

			// In ENI and AlibabaCloud ENI mode, we require the gateway, CIDRs, and the
			// ENI MAC addr in order to set up rules and routes on the local node to
			// direct ingress traffic out of the ENIs.
			if option.Config.IPAM == ipamOption.IPAMENI || option.Config.IPAM == ipamOption.IPAMAlibabaCloud {
				if ingressRouting, err := parseRoutingInfo(result); err != nil {
					log.WithError(err).Warn("Unable to allocate ingress information for ENI")
				} else {
					if err := ingressRouting.Configure(
						result.IP,
						d.mtuConfig.GetDeviceMTU(),
						option.Config.EgressMultiHomeIPRuleCompat,
						false,
					); err != nil {
						log.WithError(err).Warn("Error while configuring ingress IP rules and routes.")
					}
				}
			}
		}

		// Only allocate if enabled and not restored already
		if option.Config.EnableIPv6 {
			var result *ipam.AllocationResult
			var err error

			// Reallocate the same address as before, if possible
			ingressIPv6 := node.GetIngressIPv6()
			if ingressIPv6 != nil {
				result, err = d.ipam.AllocateIPWithoutSyncUpstream(ingressIPv6, "ingress", ipam.PoolDefault())
				if err != nil {
					log.WithError(err).WithField(logfields.SourceIP, ingressIPv6).Warn("unable to re-allocate ingress IPv6.")
					result = nil
				}
			}

			// Allocate a fresh IP if not restored, or the reallocation of the restored
			// IP failed
			if result == nil {
				result, err = d.ipam.AllocateNextFamilyWithoutSyncUpstream(ipam.IPv6, "ingress", ipam.PoolDefault())
				if err != nil {
					if ingressIPv4 := node.GetIngressIPv4(); ingressIPv4 != nil {
						d.ipam.ReleaseIP(ingressIPv4, ipam.PoolDefault())
						node.SetIngressIPv4(nil)
					}
					return fmt.Errorf("unable to allocate ingress IPs: %w, see https://cilium.link/ipam-range-full", err)
				}
			}

			// Coalescing multiple CIDRs. GH #18868
			if option.Config.EnableIPv6Masquerade &&
				option.Config.IPAM == ipamOption.IPAMENI &&
				result != nil &&
				len(result.CIDRs) > 0 {
				result.CIDRs, err = coalesceCIDRs(result.CIDRs)
				if err != nil {
					return fmt.Errorf("failed to coalesce CIDRs: %w", err)
				}
			}

			node.SetIngressIPv6(result.IP)
			log.Infof("  Ingress IPv6: %s", node.GetIngressIPv6())
		}
	}
	bootstrapStats.ingressIPAM.End(true)
	return nil
}

type restoredIPs struct {
	IPv4FromK8s, IPv4FromFS net.IP
	IPv6FromK8s, IPv6FromFS net.IP
}

func (d *Daemon) allocateIPs(ctx context.Context, router restoredIPs) error {
	bootstrapStats.ipam.Start()

	if option.Config.EnableIPv4 {
		routerIP, err := d.allocateRouterIPv4(d.nodeAddressing.IPv4(), router.IPv4FromK8s, router.IPv4FromFS)
		if err != nil {
			return err
		}
		if routerIP != nil {
			node.SetInternalIPv4Router(routerIP)
		}
	}

	if option.Config.EnableIPv6 {
		routerIP, err := d.allocateRouterIPv6(d.nodeAddressing.IPv6(), router.IPv6FromK8s, router.IPv6FromFS)
		if err != nil {
			return err
		}
		if routerIP != nil {
			node.SetIPv6Router(routerIP)
		}
	}

	// Clean up any stale IPs from the `cilium_host` interface
	removeOldCiliumHostIPs(ctx, node.GetInternalIPv4Router(), node.GetIPv6Router())

	log.Info("Addressing information:")
	log.Infof("  Cluster-Name: %s", option.Config.ClusterName)
	log.Infof("  Cluster-ID: %d", option.Config.ClusterID)
	log.Infof("  Local node-name: %s", nodeTypes.GetName())
	log.Infof("  Node-IPv6: %s", node.GetIPv6())

	iter := d.nodeAddrs.All(d.db.ReadTxn())
	addrs := statedb.Collect(
		statedb.Filter(
			iter,
			func(addr tables.NodeAddress) bool { return addr.DeviceName != tables.WildcardDeviceName }))

	if option.Config.EnableIPv6 {
		log.Infof("  IPv6 allocation prefix: %s", node.GetIPv6AllocRange())

		if c := option.Config.IPv6NativeRoutingCIDR; c != nil {
			log.Infof("  IPv6 native routing prefix: %s", c.String())
		}

		log.Infof("  IPv6 router address: %s", node.GetIPv6Router())

		log.Info("  Local IPv6 addresses:")
		for _, addr := range addrs {
			if addr.Addr.Is6() {
				log.Infof("  - %s", addr.Addr)
			}
		}
	}

	log.Infof("  External-Node IPv4: %s", node.GetIPv4())
	log.Infof("  Internal-Node IPv4: %s", node.GetInternalIPv4Router())

	if option.Config.EnableIPv4 {
		log.Infof("  IPv4 allocation prefix: %s", node.GetIPv4AllocRange())

		if c := option.Config.IPv4NativeRoutingCIDR; c != nil {
			log.Infof("  IPv4 native routing prefix: %s", c.String())
		}

		// Allocate IPv4 service loopback IP
		loopbackIPv4 := net.ParseIP(option.Config.LoopbackIPv4)
		if loopbackIPv4 == nil {
			return fmt.Errorf("Invalid IPv4 loopback address %s", option.Config.LoopbackIPv4)
		}
		node.SetIPv4Loopback(loopbackIPv4)
		log.Infof("  Loopback IPv4: %s", node.GetIPv4Loopback().String())

		log.Info("  Local IPv4 addresses:")
		for _, addr := range addrs {
			if addr.Addr.Is4() {
				log.Infof("  - %s", addr.Addr)
			}
		}
	}

	bootstrapStats.ipam.End(true)

	if option.Config.EnableEnvoyConfig {
		if err := d.allocateIngressIPs(); err != nil {
			return err
		}
	}

	return d.allocateHealthIPs()
}

func (d *Daemon) configureIPAM() {
	// If the device has been specified, the IPv4AllocPrefix and the
	// IPv6AllocPrefix were already allocated before the k8s.Init().
	//
	// If the device hasn't been specified, k8s.Init() allocated the
	// IPv4AllocPrefix and the IPv6AllocPrefix from k8s node annotations.
	//
	// If k8s.Init() failed to retrieve the IPv4AllocPrefix we can try to derive
	// it from an existing node_config.h file or from previous cilium_host
	// interfaces.
	//
	// Then, we will calculate the IPv4 or IPv6 alloc prefix based on the IPv6
	// or IPv4 alloc prefix, respectively, retrieved by k8s node annotations.
	if option.Config.IPv4Range != AutoCIDR {
		allocCIDR, err := cidr.ParseCIDR(option.Config.IPv4Range)
		if err != nil {
			log.WithError(err).WithField(logfields.V4Prefix, option.Config.IPv4Range).Fatal("Invalid IPv4 allocation prefix")
		}
		node.SetIPv4AllocRange(allocCIDR)
	}

	if option.Config.IPv6Range != AutoCIDR {
		allocCIDR, err := cidr.ParseCIDR(option.Config.IPv6Range)
		if err != nil {
			log.WithError(err).WithField(logfields.V6Prefix, option.Config.IPv6Range).Fatal("Invalid IPv6 allocation prefix")
		}

		node.SetIPv6NodeRange(allocCIDR)
	}

	device := ""
	drd, _ := d.directRoutingDev.Get(d.ctx, d.db.ReadTxn())
	if drd != nil {
		device = drd.Name
	}
	if err := node.AutoComplete(device); err != nil {
		log.WithError(err).Fatal("Cannot autocomplete node addresses")
	}
}

func (d *Daemon) startIPAM() {
	bootstrapStats.ipam.Start()
	log.Info("Initializing node addressing")
	// Set up ipam conf after init() because we might be running d.conf.KVStoreIPv4Registration
	d.ipam.ConfigureAllocator()
	bootstrapStats.ipam.End(true)
}

func parseRoutingInfo(result *ipam.AllocationResult) (*linuxrouting.RoutingInfo, error) {
	return linuxrouting.NewRoutingInfo(
		result.GatewayIP,
		result.CIDRs,
		result.PrimaryMAC,
		result.InterfaceNumber,
		option.Config.IPAM,
		option.Config.EnableIPv4Masquerade,
	)
}
