// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package features

import (
	"github.com/cilium/cilium/pkg/k8s"
	v2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	"github.com/cilium/cilium/pkg/loadbalancer"
	"github.com/cilium/cilium/pkg/redirectpolicy"
)

func (m Metrics) AddLRPConfig(_ *redirectpolicy.LRPConfig) {
	m.NPLRPIngested.WithLabelValues(actionAdd).Inc()
}

func (m Metrics) DelLRPConfig(_ *redirectpolicy.LRPConfig) {
	m.NPLRPIngested.WithLabelValues(actionDel).Inc()
}

func (m Metrics) AddService(svc *k8s.Service) {
	if svc.IntTrafficPolicy == loadbalancer.SVCTrafficPolicyLocal {
		m.ACLBInternalTrafficPolicyIngested.WithLabelValues(actionAdd).Inc()
	}
}

func (m Metrics) DelService(svc *k8s.Service) {
	if svc.IntTrafficPolicy == loadbalancer.SVCTrafficPolicyLocal {
		m.ACLBInternalTrafficPolicyIngested.WithLabelValues(actionDel).Inc()
	}
}

func (m Metrics) AddCEC(_ *v2.CiliumEnvoyConfigSpec) {
	m.ACLBCiliumEnvoyConfigIngested.WithLabelValues(actionAdd).Inc()
}

func (m Metrics) DelCEC(_ *v2.CiliumEnvoyConfigSpec) {
	m.ACLBCiliumEnvoyConfigIngested.WithLabelValues(actionDel).Inc()
}

func (m Metrics) AddCCEC(_ *v2.CiliumEnvoyConfigSpec) {
	m.ACLBCiliumClusterwideEnvoyConfigIngested.WithLabelValues(actionAdd).Inc()
}

func (m Metrics) DelCCEC(_ *v2.CiliumEnvoyConfigSpec) {
	m.ACLBCiliumClusterwideEnvoyConfigIngested.WithLabelValues(actionDel).Inc()
}
