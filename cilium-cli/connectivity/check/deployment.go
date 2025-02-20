// SPDX-License-Identifier: Apache-2.0
// Copyright Authors of Cilium

package check

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/netip"
	"slices"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/cilium/cilium/cilium-cli/defaults"
	"github.com/cilium/cilium/cilium-cli/k8s"
	"github.com/cilium/cilium/cilium-cli/utils/features"
	ciliumv2 "github.com/cilium/cilium/pkg/k8s/apis/cilium.io/v2"
	slimmetav1 "github.com/cilium/cilium/pkg/k8s/slim/k8s/apis/meta/v1"
	policyapi "github.com/cilium/cilium/pkg/policy/api"
	"github.com/cilium/cilium/pkg/versioncheck"
)

const (
	PerfHostName                          = "-host-net"
	PerfOtherNode                         = "-other-node"
	PerfLowPriority                       = "-low-priority"
	PerfHighPriority                      = "-high-priority"
	perfClientDeploymentName              = "perf-client"
	perfClientHostNetDeploymentName       = perfClientDeploymentName + PerfHostName
	perfClientAcrossDeploymentName        = perfClientDeploymentName + PerfOtherNode
	perClientLowPriorityDeploymentName    = perfClientDeploymentName + PerfLowPriority
	perClientHighPriorityDeploymentName   = perfClientDeploymentName + PerfHighPriority
	perfClientHostNetAcrossDeploymentName = perfClientAcrossDeploymentName + PerfHostName
	perfServerDeploymentName              = "perf-server"
	perfServerHostNetDeploymentName       = perfServerDeploymentName + PerfHostName

	clientDeploymentName  = "client"
	client2DeploymentName = "client2"
	client3DeploymentName = "client3"
	clientCPDeployment    = "client-cp"

	DNSTestServerContainerName = "dns-test-server"

	echoSameNodeDeploymentName                 = "echo-same-node"
	echoOtherNodeDeploymentName                = "echo-other-node"
	EchoOtherNodeDeploymentHeadlessServiceName = "echo-other-node-headless"
	echoExternalNodeDeploymentName             = "echo-external-node"
	corednsConfigMapName                       = "coredns-configmap"
	corednsConfigVolumeName                    = "coredns-config-volume"
	kindEchoName                               = "echo"
	kindEchoExternalNodeName                   = "echo-external-node"
	kindClientName                             = "client"
	kindPerfName                               = "perf"
	lrpBackendDeploymentName                   = "lrp-backend"
	lrpClientDeploymentName                    = "lrp-client"
	kindLrpName                                = "lrp"

	hostNetNSDeploymentName          = "host-netns"
	hostNetNSDeploymentNameNonCilium = "host-netns-non-cilium" // runs on non-Cilium test nodes
	kindHostNetNS                    = "host-netns"

	testConnDisruptClientDeploymentName          = "test-conn-disrupt-client"
	testConnDisruptClientNSTrafficDeploymentName = "test-conn-disrupt-client"
	testConnDisruptServerDeploymentName          = "test-conn-disrupt-server"
	testConnDisruptServerNSTrafficDeploymentName = "test-conn-disrupt-server-ns-traffic"
	testConnDisruptServiceName                   = "test-conn-disrupt"
	testConnDisruptNSTrafficServiceName          = "test-conn-disrupt-ns-traffic"
	testConnDisruptCNPName                       = "test-conn-disrupt"
	testConnDisruptNSTrafficCNPName              = "test-conn-disrupt-ns-traffic"
	testConnDisruptServerNSTrafficAppLabel       = "test-conn-disrupt-server-ns-traffic"
	KindTestConnDisrupt                          = "test-conn-disrupt"
	KindTestConnDisruptNSTraffic                 = "test-conn-disrupt-ns-traffic"

	bwPrioAnnotationString = "bandwidth.cilium.io/priority"
)

var (
	appLabels = map[string]string{
		"app.kubernetes.io/name": "cilium-cli",
	}
)

type deploymentParameters struct {
	Name                          string
	Kind                          string
	Image                         string
	Replicas                      int
	NamedPort                     string
	Port                          int
	HostPort                      int
	Command                       []string
	Affinity                      *corev1.Affinity
	NodeSelector                  map[string]string
	ReadinessProbe                *corev1.Probe
	Resources                     corev1.ResourceRequirements
	Labels                        map[string]string
	Annotations                   map[string]string
	HostNetwork                   bool
	Tolerations                   []corev1.Toleration
	TerminationGracePeriodSeconds *int64
}

func (p *deploymentParameters) namedPort() string {
	if len(p.NamedPort) == 0 {
		return fmt.Sprintf("port-%d", p.Port)
	}
	return p.NamedPort
}

func (p *deploymentParameters) ports() (ports []corev1.ContainerPort) {
	if p.Port != 0 {
		ports = append(ports, corev1.ContainerPort{
			Name: p.namedPort(), ContainerPort: int32(p.Port), HostPort: int32(p.HostPort)})
	}

	return ports
}

func (p *deploymentParameters) envs() (envs []corev1.EnvVar) {
	if p.Port != 0 {
		envs = append(envs,
			corev1.EnvVar{Name: "PORT", Value: fmt.Sprintf("%d", p.Port)},
			corev1.EnvVar{Name: "NAMED_PORT", Value: p.namedPort()},
		)
	}

	return envs
}

func newDeployment(p deploymentParameters) *appsv1.Deployment {
	if p.Replicas == 0 {
		p.Replicas = 1
	}
	replicas32 := int32(p.Replicas)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name: p.Name,
			Labels: map[string]string{
				"name": p.Name,
				"kind": p.Kind,
			},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: p.Name,
					Labels: map[string]string{
						"name": p.Name,
						"kind": p.Kind,
					},
					Annotations: p.Annotations,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            p.Name,
							Env:             p.envs(),
							Ports:           p.ports(),
							Image:           p.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         p.Command,
							ReadinessProbe:  p.ReadinessProbe,
							Resources:       p.Resources,
							SecurityContext: &corev1.SecurityContext{
								Capabilities: &corev1.Capabilities{
									Add: []corev1.Capability{"NET_RAW"},
								},
							},
						},
					},
					Affinity:                      p.Affinity,
					NodeSelector:                  p.NodeSelector,
					HostNetwork:                   p.HostNetwork,
					Tolerations:                   p.Tolerations,
					ServiceAccountName:            p.Name,
					TerminationGracePeriodSeconds: p.TerminationGracePeriodSeconds,
				},
			},
			Replicas: &replicas32,
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": p.Name,
					"kind": p.Kind,
				},
			},
		},
	}

	for k, v := range p.Labels {
		dep.Spec.Template.ObjectMeta.Labels[k] = v
	}

	return dep
}

func newDeploymentWithDNSTestServer(p deploymentParameters, DNSTestServerImage string) *appsv1.Deployment {
	dep := newDeployment(p)

	dep.Spec.Template.Spec.Containers = append(
		dep.Spec.Template.Spec.Containers,
		corev1.Container{
			Args: []string{"-conf", "/etc/coredns/Corefile"},
			Name: DNSTestServerContainerName,
			Ports: []corev1.ContainerPort{
				{ContainerPort: 53, Name: "dns-53"},
				{ContainerPort: 53, Name: "dns-udp-53", Protocol: corev1.ProtocolUDP},
			},
			Image:           DNSTestServerImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			ReadinessProbe:  newLocalReadinessProbe(8181, "/ready"),
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      corednsConfigVolumeName,
					MountPath: "/etc/coredns",
					ReadOnly:  true,
				},
			},
		},
	)
	dep.Spec.Template.Spec.Volumes = []corev1.Volume{
		{
			Name: corednsConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: corednsConfigMapName,
					},
					Items: []corev1.KeyToPath{
						{
							Key:  "Corefile",
							Path: "Corefile",
						},
					},
				},
			},
		},
	}

	return dep
}

type daemonSetParameters struct {
	Name           string
	Kind           string
	Image          string
	Replicas       int
	Command        []string
	Affinity       *corev1.Affinity
	ReadinessProbe *corev1.Probe
	Labels         map[string]string
	HostNetwork    bool
	Tolerations    []corev1.Toleration
	Capabilities   []corev1.Capability
	NodeSelector   map[string]string
}

func newDaemonSet(p daemonSetParameters) *appsv1.DaemonSet {
	ds := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name: p.Name,
			Labels: map[string]string{
				"name": p.Name,
				"kind": p.Kind,
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Name: p.Name,
					Labels: map[string]string{
						"name": p.Name,
						"kind": p.Kind,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:            p.Name,
							Image:           p.Image,
							ImagePullPolicy: corev1.PullIfNotPresent,
							Command:         p.Command,
							ReadinessProbe:  p.ReadinessProbe,
							SecurityContext: &corev1.SecurityContext{
								Capabilities: &corev1.Capabilities{
									Add: append([]corev1.Capability{"NET_RAW"}, p.Capabilities...),
								},
							},
						},
					},
					Affinity:    p.Affinity,
					HostNetwork: p.HostNetwork,
					Tolerations: p.Tolerations,
				},
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"name": p.Name,
					"kind": p.Kind,
				},
			},
		},
	}

	for k, v := range p.Labels {
		ds.Spec.Template.ObjectMeta.Labels[k] = v
	}

	if p.NodeSelector != nil {
		ds.Spec.Template.Spec.NodeSelector = p.NodeSelector
	}

	return ds
}

var serviceLabels = map[string]string{
	"kind": kindEchoName,
}

func newService(name string, selector map[string]string, labels map[string]string, portName string, port int, serviceType string) *corev1.Service {
	ipFamPol := corev1.IPFamilyPolicyPreferDualStack
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceType(serviceType),
			Ports: []corev1.ServicePort{
				{Name: portName, Port: int32(port)},
			},
			Selector:       selector,
			IPFamilyPolicy: &ipFamPol,
		},
	}
}

func newLocalReadinessProbe(port int, path string) *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Path:   path,
				Port:   intstr.FromInt(port),
				Scheme: corev1.URISchemeHTTP,
			},
		},
		TimeoutSeconds:      int32(2),
		SuccessThreshold:    int32(1),
		PeriodSeconds:       int32(1),
		InitialDelaySeconds: int32(1),
		FailureThreshold:    int32(3),
	}
}

func newIngress(name, backend string) *networkingv1.Ingress {
	return &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Annotations: map[string]string{
				"ingress.cilium.io/loadbalancer-mode": "dedicated",
				"ingress.cilium.io/service-type":      "NodePort",
			},
		},
		Spec: networkingv1.IngressSpec{
			IngressClassName: func(in string) *string {
				return &in
			}(defaults.IngressClassName),
			Rules: []networkingv1.IngressRule{
				{
					IngressRuleValue: networkingv1.IngressRuleValue{
						HTTP: &networkingv1.HTTPIngressRuleValue{
							Paths: []networkingv1.HTTPIngressPath{
								{
									Path: "/",
									PathType: func() *networkingv1.PathType {
										pt := networkingv1.PathTypeImplementationSpecific
										return &pt
									}(),
									Backend: networkingv1.IngressBackend{
										Service: &networkingv1.IngressServiceBackend{
											Name: backend,
											Port: networkingv1.ServiceBackendPort{
												Number: 8080,
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

func newConnDisruptCNP(ns string) *ciliumv2.CiliumNetworkPolicy {
	selector := policyapi.EndpointSelector{
		LabelSelector: &slimmetav1.LabelSelector{
			MatchLabels: map[string]string{"kind": KindTestConnDisrupt},
		},
	}

	ports := []policyapi.PortRule{{
		Ports: []policyapi.PortProtocol{{
			Protocol: policyapi.ProtoTCP,
			Port:     "8000",
		}},
	}}

	return &ciliumv2.CiliumNetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       ciliumv2.CNPKindDefinition,
			APIVersion: ciliumv2.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{Name: testConnDisruptCNPName, Namespace: ns},
		Spec: &policyapi.Rule{
			EndpointSelector: selector,
			Egress: []policyapi.EgressRule{
				{
					EgressCommonRule: policyapi.EgressCommonRule{
						ToEndpoints: []policyapi.EndpointSelector{selector},
					},
					ToPorts: ports,
				},
				{
					// Allow access to DNS for service resolution (we don't care
					// of being restrictive here, so we just allow all endpoints).
					ToPorts: []policyapi.PortRule{{
						Ports: []policyapi.PortProtocol{
							{Protocol: policyapi.ProtoUDP, Port: "53"},
							{Protocol: policyapi.ProtoUDP, Port: "5353"},
						},
					}},
				},
			},
			Ingress: []policyapi.IngressRule{{
				IngressCommonRule: policyapi.IngressCommonRule{
					FromEndpoints: []policyapi.EndpointSelector{selector},
				},
				ToPorts: ports,
			}},
		},
	}
}

func newConnDisruptCNPForNSTraffic(ns string) *ciliumv2.CiliumNetworkPolicy {
	selector := policyapi.EndpointSelector{
		LabelSelector: &slimmetav1.LabelSelector{
			MatchLabels: map[string]string{"kind": KindTestConnDisruptNSTraffic},
		},
	}

	ports := []policyapi.PortRule{{
		Ports: []policyapi.PortProtocol{{
			Protocol: policyapi.ProtoTCP,
			Port:     "8000",
		}},
	}}

	return &ciliumv2.CiliumNetworkPolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       ciliumv2.CNPKindDefinition,
			APIVersion: ciliumv2.SchemeGroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{Name: testConnDisruptNSTrafficCNPName, Namespace: ns},
		Spec: &policyapi.Rule{
			EndpointSelector: selector,
			Ingress: []policyapi.IngressRule{{
				IngressCommonRule: policyapi.IngressCommonRule{
					FromEntities: policyapi.EntitySlice{
						policyapi.EntityWorld,
						policyapi.EntityRemoteNode,
					},
				},
				ToPorts: ports,
			}},
		},
	}
}

func (ct *ConnectivityTest) ingresses() map[string]string {
	ingresses := map[string]string{"same-node": echoSameNodeDeploymentName}
	if !ct.Params().SingleNode || ct.Params().MultiCluster != "" {
		ingresses["other-node"] = echoOtherNodeDeploymentName
	}
	return ingresses
}

// maybeNodeToNodeEncryptionAffinity returns a node affinity term to prefer nodes
// not being part of the control plane when node to node encryption is enabled,
// because they are excluded by default from node to node encryption. This logic
// is currently suboptimal as it only accounts for the default selector, for the
// sake of simplicity, but it should cover all common use cases.
func (ct *ConnectivityTest) maybeNodeToNodeEncryptionAffinity() *corev1.NodeAffinity {
	encryptNode, _ := ct.Feature(features.EncryptionNode)
	if !encryptNode.Enabled || encryptNode.Mode == "" {
		return nil
	}

	return &corev1.NodeAffinity{
		PreferredDuringSchedulingIgnoredDuringExecution: []corev1.PreferredSchedulingTerm{{
			Weight: 100,
			Preference: corev1.NodeSelectorTerm{
				MatchExpressions: []corev1.NodeSelectorRequirement{{
					Key:      "node-role.kubernetes.io/control-plane",
					Operator: corev1.NodeSelectorOpDoesNotExist,
				}},
			},
		}},
	}
}

// deploy ensures the test Namespace, Services and Deployments are running on the cluster.
func (ct *ConnectivityTest) deploy(ctx context.Context) error {
	var err error

	for _, client := range ct.Clients() {
		if ct.params.ForceDeploy {
			if err := ct.deleteDeployments(ctx, client); err != nil {
				return err
			}
			if err := ct.DeleteConnDisruptTestDeployment(ctx, client); err != nil {
				return err
			}
		}

		_, err := client.GetNamespace(ctx, ct.params.TestNamespace, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Creating namespace %s for connectivity check...", client.ClusterName(), ct.params.TestNamespace)
			namespace := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{
					Name:        ct.params.TestNamespace,
					Annotations: ct.params.NamespaceAnnotations,
					Labels:      appLabels,
				},
			}
			_, err = client.CreateNamespace(ctx, namespace, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create namespace %s: %w", ct.params.TestNamespace, err)
			}
		}
	}

	// Deploy perf actors (only in the first test namespace
	// in case of tests concurrent run)
	if ct.params.Perf && ct.params.TestNamespaceIndex == 0 {
		return ct.deployPerf(ctx)
	}

	// Deploy test-conn-disrupt actors (only in the first
	// test namespace in case of tests concurrent run)
	if ct.params.ConnDisruptTestSetup && ct.params.TestNamespaceIndex == 0 {
		if err := ct.createTestConnDisruptServerDeployAndSvc(ctx, testConnDisruptServerDeploymentName, KindTestConnDisrupt, 3,
			testConnDisruptServiceName, "test-conn-disrupt-server", newConnDisruptCNP); err != nil {
			return err
		}

		if err := ct.createTestConnDisruptClientDeployment(ctx, testConnDisruptClientDeploymentName, KindTestConnDisrupt,
			"test-conn-disrupt-client", fmt.Sprintf("test-conn-disrupt.%s.svc.cluster.local.:8000", ct.params.TestNamespace),
			5, false); err != nil {
			return err
		}

		if ct.ShouldRunConnDisruptNSTraffic() {
			if err := ct.createTestConnDisruptServerDeployAndSvc(ctx, testConnDisruptServerNSTrafficDeploymentName, KindTestConnDisruptNSTraffic, 1,
				testConnDisruptNSTrafficServiceName, testConnDisruptServerNSTrafficAppLabel, newConnDisruptCNPForNSTraffic); err != nil {
				return err
			}

			if err := ct.createTestConnDisruptClientDeploymentForNSTraffic(ctx); err != nil {
				return err
			}
		} else {
			ct.Info("Skipping conn-disrupt-test for NS traffic")
		}
	}

	_, err = ct.clients.src.GetService(ctx, ct.params.TestNamespace, echoSameNodeDeploymentName, metav1.GetOptions{})
	if err != nil {
		ct.Logf("✨ [%s] Deploying %s service...", ct.clients.src.ClusterName(), echoSameNodeDeploymentName)
		svc := newService(echoSameNodeDeploymentName, map[string]string{"name": echoSameNodeDeploymentName}, serviceLabels, "http", 8080, ct.Params().ServiceType)
		_, err = ct.clients.src.CreateService(ctx, ct.params.TestNamespace, svc, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	}

	if ct.params.MultiCluster != "" {
		_, err = ct.clients.src.GetService(ctx, ct.params.TestNamespace, echoOtherNodeDeploymentName, metav1.GetOptions{})
		svc := newService(echoOtherNodeDeploymentName, map[string]string{"name": echoOtherNodeDeploymentName}, serviceLabels, "http", 8080, ct.Params().ServiceType)
		svc.ObjectMeta.Annotations = map[string]string{}
		svc.ObjectMeta.Annotations["service.cilium.io/global"] = "true"
		svc.ObjectMeta.Annotations["io.cilium/global-service"] = "true"

		if err != nil {
			ct.Logf("✨ [%s] Deploying %s service...", ct.clients.src.ClusterName(), echoOtherNodeDeploymentName)
			_, err = ct.clients.src.CreateService(ctx, ct.params.TestNamespace, svc, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		}

		_, err = ct.clients.src.GetService(ctx, ct.params.TestNamespace, EchoOtherNodeDeploymentHeadlessServiceName, metav1.GetOptions{})
		svcHeadless := svc.DeepCopy()
		svcHeadless.Name = EchoOtherNodeDeploymentHeadlessServiceName
		svcHeadless.Spec.ClusterIP = corev1.ClusterIPNone
		svcHeadless.Spec.Type = corev1.ServiceTypeClusterIP
		svcHeadless.ObjectMeta.Annotations["service.cilium.io/global-sync-endpoint-slices"] = "true"

		if err != nil {
			ct.Logf("✨ [%s] Deploying %s service...", ct.clients.src.ClusterName(), EchoOtherNodeDeploymentHeadlessServiceName)
			_, err = ct.clients.src.CreateService(ctx, ct.params.TestNamespace, svcHeadless, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		}
	}

	hostPort := 0
	if ct.Features[features.HostPort].Enabled {
		hostPort = ct.Params().EchoServerHostPort
	}
	dnsConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name: corednsConfigMapName,
		},
		Data: map[string]string{
			"Corefile": `. {
				local
				ready
				log
			}`,
		},
	}
	_, err = ct.clients.src.GetConfigMap(ctx, ct.params.TestNamespace, corednsConfigMapName, metav1.GetOptions{})
	if err != nil {
		ct.Logf("✨ [%s] Deploying DNS test server configmap...", ct.clients.src.ClusterName())
		_, err = ct.clients.src.CreateConfigMap(ctx, ct.params.TestNamespace, dnsConfigMap, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create configmap %s: %w", corednsConfigMapName, err)
		}
	}
	if ct.params.MultiCluster != "" {
		_, err = ct.clients.dst.GetConfigMap(ctx, ct.params.TestNamespace, corednsConfigMapName, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Deploying DNS test server configmap...", ct.clients.dst.ClusterName())
			_, err = ct.clients.dst.CreateConfigMap(ctx, ct.params.TestNamespace, dnsConfigMap, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create configmap %s: %w", corednsConfigMapName, err)
			}
		}
	}

	_, err = ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, echoSameNodeDeploymentName, metav1.GetOptions{})
	if err != nil {
		ct.Logf("✨ [%s] Deploying same-node deployment...", ct.clients.src.ClusterName())
		containerPort := 8080
		echoDeployment := newDeploymentWithDNSTestServer(deploymentParameters{
			Name:        echoSameNodeDeploymentName,
			Kind:        kindEchoName,
			Port:        containerPort,
			NamedPort:   "http-8080",
			HostPort:    hostPort,
			Image:       ct.params.JSONMockImage,
			Labels:      map[string]string{"other": "echo"},
			Annotations: ct.params.DeploymentAnnotations.Match(echoSameNodeDeploymentName),
			Affinity: &corev1.Affinity{
				PodAffinity: &corev1.PodAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{Key: "name", Operator: metav1.LabelSelectorOpIn, Values: []string{clientDeploymentName}},
								},
							},
							TopologyKey: corev1.LabelHostname,
						},
					},
				},
				NodeAffinity: ct.maybeNodeToNodeEncryptionAffinity(),
			},
			ReadinessProbe: newLocalReadinessProbe(containerPort, "/"),
		}, ct.params.DNSTestServerImage)
		_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(echoSameNodeDeploymentName), metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create service account %s: %w", echoSameNodeDeploymentName, err)
		}
		_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, echoDeployment, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create deployment %s: %w", echoSameNodeDeploymentName, err)
		}
	}

	_, err = ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, clientDeploymentName, metav1.GetOptions{})
	if err != nil {
		ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.src.ClusterName(), clientDeploymentName)
		clientDeployment := newDeployment(deploymentParameters{
			Name:         clientDeploymentName,
			Kind:         kindClientName,
			Image:        ct.params.CurlImage,
			Command:      []string{"/usr/bin/pause"},
			Annotations:  ct.params.DeploymentAnnotations.Match(clientDeploymentName),
			Affinity:     &corev1.Affinity{NodeAffinity: ct.maybeNodeToNodeEncryptionAffinity()},
			NodeSelector: ct.params.NodeSelector,
		})
		_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(clientDeploymentName), metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create service account %s: %w", clientDeploymentName, err)
		}
		_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, clientDeployment, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create deployment %s: %w", clientDeploymentName, err)
		}
	}

	// 2nd client with label other=client
	_, err = ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, client2DeploymentName, metav1.GetOptions{})
	if err != nil {
		ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.src.ClusterName(), client2DeploymentName)
		clientDeployment := newDeployment(deploymentParameters{
			Name:        client2DeploymentName,
			Kind:        kindClientName,
			Image:       ct.params.CurlImage,
			Command:     []string{"/usr/bin/pause"},
			Labels:      map[string]string{"other": "client"},
			Annotations: ct.params.DeploymentAnnotations.Match(client2DeploymentName),
			Affinity: &corev1.Affinity{
				PodAffinity: &corev1.PodAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{Key: "name", Operator: metav1.LabelSelectorOpIn, Values: []string{clientDeploymentName}},
								},
							},
							TopologyKey: corev1.LabelHostname,
						},
					},
				},
				NodeAffinity: ct.maybeNodeToNodeEncryptionAffinity(),
			},
			NodeSelector: ct.params.NodeSelector,
		})
		_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(client2DeploymentName), metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create service account %s: %w", client2DeploymentName, err)
		}
		_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, clientDeployment, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create deployment %s: %w", client2DeploymentName, err)
		}
	}

	if ct.params.MultiCluster == "" && !ct.params.SingleNode {
		// 3rd client scheduled on a different node than the first 2 clients
		_, err = ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, client3DeploymentName, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.src.ClusterName(), client3DeploymentName)
			clientDeployment := newDeployment(deploymentParameters{
				Name:        client3DeploymentName,
				Kind:        kindClientName,
				Image:       ct.params.CurlImage,
				Command:     []string{"/usr/bin/pause"},
				Labels:      map[string]string{"other": "client-other-node"},
				Annotations: ct.params.DeploymentAnnotations.Match(client3DeploymentName),
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{Key: "name", Operator: metav1.LabelSelectorOpIn, Values: []string{clientDeploymentName}},
									},
								},
								TopologyKey: corev1.LabelHostname,
							},
						},
					},
					NodeAffinity: ct.maybeNodeToNodeEncryptionAffinity(),
				},
				NodeSelector: ct.params.NodeSelector,
			})
			_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(client3DeploymentName), metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create service account %s: %w", client3DeploymentName, err)
			}
			_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, clientDeployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create deployment %s: %w", client3DeploymentName, err)
			}
		}
	}

	// 4th client scheduled on the control plane
	if ct.params.K8sLocalHostTest {
		ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.src.ClusterName(), clientCPDeployment)
		clientDeployment := newDeployment(deploymentParameters{
			Name:        clientCPDeployment,
			Kind:        kindClientName,
			Image:       ct.params.CurlImage,
			Command:     []string{"/usr/bin/pause"},
			Labels:      map[string]string{"other": "client"},
			Annotations: ct.params.DeploymentAnnotations.Match(client2DeploymentName),
			NodeSelector: map[string]string{
				"node-role.kubernetes.io/control-plane": "",
			},
			Replicas: len(ct.ControlPlaneNodes()),
			Tolerations: []corev1.Toleration{
				{Key: "node-role.kubernetes.io/control-plane"},
			},
		})
		_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(clientCPDeployment), metav1.CreateOptions{})
		if err != nil && !k8sErrors.IsAlreadyExists(err) {
			return fmt.Errorf("unable to create service account %s: %w", clientCPDeployment, err)
		}
		_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, clientDeployment, metav1.CreateOptions{})
		if err != nil && !k8sErrors.IsAlreadyExists(err) {
			return fmt.Errorf("unable to create deployment %s: %w", clientCPDeployment, err)
		}
	}

	if !ct.params.SingleNode || ct.params.MultiCluster != "" {

		_, err = ct.clients.dst.GetService(ctx, ct.params.TestNamespace, echoOtherNodeDeploymentName, metav1.GetOptions{})
		svc := newService(echoOtherNodeDeploymentName, map[string]string{"name": echoOtherNodeDeploymentName}, serviceLabels, "http", 8080, ct.Params().ServiceType)
		if ct.params.MultiCluster != "" {
			svc.ObjectMeta.Annotations = map[string]string{}
			svc.ObjectMeta.Annotations["service.cilium.io/global"] = "true"
			svc.ObjectMeta.Annotations["io.cilium/global-service"] = "true"
		}

		if err != nil {
			ct.Logf("✨ [%s] Deploying %s service...", ct.clients.dst.ClusterName(), echoOtherNodeDeploymentName)
			_, err = ct.clients.dst.CreateService(ctx, ct.params.TestNamespace, svc, metav1.CreateOptions{})
			if err != nil {
				return err
			}
		}

		if ct.params.MultiCluster != "" {
			svcHeadless := svc.DeepCopy()
			svcHeadless.Name = EchoOtherNodeDeploymentHeadlessServiceName
			svcHeadless.Spec.ClusterIP = corev1.ClusterIPNone
			svcHeadless.Spec.Type = corev1.ServiceTypeClusterIP
			svcHeadless.ObjectMeta.Annotations["service.cilium.io/global-sync-endpoint-slices"] = "true"
			_, err = ct.clients.dst.GetService(ctx, ct.params.TestNamespace, EchoOtherNodeDeploymentHeadlessServiceName, metav1.GetOptions{})

			if err != nil {
				ct.Logf("✨ [%s] Deploying %s service...", ct.clients.dst.ClusterName(), EchoOtherNodeDeploymentHeadlessServiceName)
				_, err = ct.clients.dst.CreateService(ctx, ct.params.TestNamespace, svcHeadless, metav1.CreateOptions{})
				if err != nil {
					return err
				}
			}
		}

		_, err = ct.clients.dst.GetDeployment(ctx, ct.params.TestNamespace, echoOtherNodeDeploymentName, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Deploying other-node deployment...", ct.clients.dst.ClusterName())
			containerPort := 8080
			echoOtherNodeDeployment := newDeploymentWithDNSTestServer(deploymentParameters{
				Name:        echoOtherNodeDeploymentName,
				Kind:        kindEchoName,
				NamedPort:   "http-8080",
				Port:        containerPort,
				HostPort:    hostPort,
				Image:       ct.params.JSONMockImage,
				Labels:      map[string]string{"first": "echo"},
				Annotations: ct.params.DeploymentAnnotations.Match(echoOtherNodeDeploymentName),
				Affinity: &corev1.Affinity{
					PodAntiAffinity: &corev1.PodAntiAffinity{
						RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
							{
								LabelSelector: &metav1.LabelSelector{
									MatchExpressions: []metav1.LabelSelectorRequirement{
										{Key: "name", Operator: metav1.LabelSelectorOpIn, Values: []string{clientDeploymentName}},
									},
								},
								TopologyKey: corev1.LabelHostname,
							},
						},
					},
					NodeAffinity: ct.maybeNodeToNodeEncryptionAffinity(),
				},
				NodeSelector:   ct.params.NodeSelector,
				ReadinessProbe: newLocalReadinessProbe(containerPort, "/"),
			}, ct.params.DNSTestServerImage)
			_, err = ct.clients.dst.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(echoOtherNodeDeploymentName), metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create service account %s: %w", echoOtherNodeDeploymentName, err)
			}
			_, err = ct.clients.dst.CreateDeployment(ctx, ct.params.TestNamespace, echoOtherNodeDeployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create deployment %s: %w", echoOtherNodeDeploymentName, err)
			}
		}

		for _, client := range ct.clients.clients() {
			_, err = client.GetDaemonSet(ctx, ct.params.TestNamespace, hostNetNSDeploymentName, metav1.GetOptions{})
			if err != nil {
				ct.Logf("✨ [%s] Deploying %s daemonset...", hostNetNSDeploymentName, client.ClusterName())
				ds := newDaemonSet(daemonSetParameters{
					Name:        hostNetNSDeploymentName,
					Kind:        kindHostNetNS,
					Image:       ct.params.CurlImage,
					Labels:      map[string]string{"other": "host-netns"},
					Command:     []string{"/usr/bin/pause"},
					HostNetwork: true,
				})
				_, err = client.CreateDaemonSet(ctx, ct.params.TestNamespace, ds, metav1.CreateOptions{})
				if err != nil {
					return fmt.Errorf("unable to create daemonset %s: %w", hostNetNSDeploymentName, err)
				}
			}
		}

		_, err = ct.clients.src.GetDaemonSet(ctx, ct.params.TestNamespace, hostNetNSDeploymentNameNonCilium, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Deploying %s daemonset...", hostNetNSDeploymentNameNonCilium, ct.clients.src.ClusterName())
			ds := newDaemonSet(daemonSetParameters{
				Name:        hostNetNSDeploymentNameNonCilium,
				Kind:        kindHostNetNS,
				Image:       ct.params.CurlImage,
				Labels:      map[string]string{"other": "host-netns"},
				Command:     []string{"/usr/bin/pause"},
				HostNetwork: true,
				Tolerations: []corev1.Toleration{
					{Operator: corev1.TolerationOpExists},
				},
				Capabilities: []corev1.Capability{corev1.Capability("NET_ADMIN")}, // to install IP routes
				NodeSelector: map[string]string{
					defaults.CiliumNoScheduleLabel: "true",
				},
			})
			_, err = ct.clients.src.CreateDaemonSet(ctx, ct.params.TestNamespace, ds, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create daemonset %s: %w", hostNetNSDeploymentNameNonCilium, err)
			}
		}

		if ct.Features[features.NodeWithoutCilium].Enabled {
			_, err = ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, echoExternalNodeDeploymentName, metav1.GetOptions{})
			if err != nil {
				ct.Logf("✨ [%s] Deploying echo-external-node deployment...", ct.clients.src.ClusterName())
				// in case if test concurrency is > 1 port must be unique for each test namespace
				port := ct.Params().ExternalDeploymentPort
				echoExternalDeployment := newDeployment(deploymentParameters{
					Name:           echoExternalNodeDeploymentName,
					Kind:           kindEchoExternalNodeName,
					Port:           port,
					NamedPort:      fmt.Sprintf("http-%d", port),
					HostPort:       port,
					Image:          ct.params.JSONMockImage,
					Labels:         map[string]string{"external": "echo"},
					Annotations:    ct.params.DeploymentAnnotations.Match(echoExternalNodeDeploymentName),
					NodeSelector:   map[string]string{"cilium.io/no-schedule": "true"},
					ReadinessProbe: newLocalReadinessProbe(port, "/"),
					HostNetwork:    true,
					Tolerations: []corev1.Toleration{
						{Operator: corev1.TolerationOpExists},
					},
				})
				_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(echoExternalNodeDeploymentName), metav1.CreateOptions{})
				if err != nil {
					return fmt.Errorf("unable to create service account %s: %w", echoExternalNodeDeploymentName, err)
				}
				_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, echoExternalDeployment, metav1.CreateOptions{})
				if err != nil {
					return fmt.Errorf("unable to create deployment %s: %w", echoExternalNodeDeploymentName, err)
				}

				svc := newService(echoExternalNodeDeploymentName,
					map[string]string{"name": echoExternalNodeDeploymentName, "kind": kindEchoExternalNodeName},
					map[string]string{"kind": kindEchoExternalNodeName}, "http", port, "ClusterIP")
				svc.Spec.ClusterIP = corev1.ClusterIPNone
				_, err := ct.clients.src.CreateService(ctx, ct.params.TestNamespace, svc, metav1.CreateOptions{})
				if err != nil {
					return fmt.Errorf("unable to create service %s: %w", echoExternalNodeDeploymentName, err)
				}
			}
		} else {
			ct.Infof("Skipping tests that require a node Without Cilium")
		}
	}

	// Create one Ingress service for echo deployment
	if ct.Features[features.IngressController].Enabled {
		for name, backend := range ct.ingresses() {
			_, err = ct.clients.src.GetIngress(ctx, ct.params.TestNamespace, name, metav1.GetOptions{})
			if err != nil {
				ct.Logf("✨ [%s] Deploying Ingress resource...", ct.clients.src.ClusterName())
				_, err = ct.clients.src.CreateIngress(ctx, ct.params.TestNamespace, newIngress(name, backend), metav1.CreateOptions{})
				if err != nil {
					return err
				}
			}
		}
	}

	if ct.Features[features.LocalRedirectPolicy].Enabled {
		ct.Logf("✨ [%s] Deploying lrp-client deployment...", ct.clients.src.ClusterName())
		lrpClientDeployment := newDeployment(deploymentParameters{
			Name:         lrpClientDeploymentName,
			Kind:         kindLrpName,
			Image:        ct.params.CurlImage,
			Command:      []string{"/usr/bin/pause"},
			Labels:       map[string]string{"lrp": "client"},
			Annotations:  ct.params.DeploymentAnnotations.Match(lrpClientDeploymentName),
			NodeSelector: ct.params.NodeSelector,
		})

		_, err = ct.clients.src.GetServiceAccount(ctx, ct.params.TestNamespace, lrpClientDeploymentName, metav1.GetOptions{})
		if err != nil {
			_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(lrpClientDeploymentName), metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create service account %s: %w", lrpClientDeployment, err)
			}
		}

		_, err = ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, lrpClientDeploymentName, metav1.GetOptions{})
		if err != nil {
			_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, lrpClientDeployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create deployment %s: %w", lrpClientDeployment, err)
			}
		}

		ct.Logf("✨ [%s] Deploying lrp-backend deployment...", ct.clients.src.ClusterName())
		containerPort := 8080
		lrpBackendDeployment := newDeployment(deploymentParameters{
			Name:           lrpBackendDeploymentName,
			Kind:           kindLrpName,
			Image:          ct.params.JSONMockImage,
			NamedPort:      "tcp-8080",
			Port:           containerPort,
			ReadinessProbe: newLocalReadinessProbe(containerPort, "/"),
			Labels:         map[string]string{"lrp": "backend"},
			Annotations:    ct.params.DeploymentAnnotations.Match(lrpBackendDeploymentName),
			Affinity: &corev1.Affinity{
				PodAffinity: &corev1.PodAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
						{
							LabelSelector: &metav1.LabelSelector{
								MatchExpressions: []metav1.LabelSelectorRequirement{
									{Key: "name", Operator: metav1.LabelSelectorOpIn, Values: []string{lrpClientDeploymentName}},
								},
							},
							TopologyKey: corev1.LabelHostname,
						},
					},
				},
			},
			NodeSelector: ct.params.NodeSelector,
		})

		_, err = ct.clients.src.GetServiceAccount(ctx, ct.params.TestNamespace, lrpBackendDeploymentName, metav1.GetOptions{})
		if err != nil {
			_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(lrpBackendDeploymentName), metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create service account %s: %w", lrpBackendDeployment, err)
			}
		}

		_, err = ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, lrpBackendDeploymentName, metav1.GetOptions{})
		if err != nil {
			_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, lrpBackendDeployment, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create deployment %s: %w", lrpBackendDeployment, err)
			}
		}
	}

	if ct.Features[features.BGPControlPlane].Enabled && ct.Features[features.NodeWithoutCilium].Enabled && ct.params.TestConcurrency == 1 {
		// BGP tests need to run sequentially, deploy only if BGP CP is enabled and test concurrency is disabled
		_, err = ct.clients.src.GetDaemonSet(ctx, ct.params.TestNamespace, frrDaemonSetNameName, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Deploying %s daemonset...", ct.clients.src.ClusterName(), frrDaemonSetNameName)
			ds := NewFRRDaemonSet(ct.params)
			_, err = ct.clients.src.CreateDaemonSet(ctx, ct.params.TestNamespace, ds, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create daemonset %s: %w", frrDaemonSetNameName, err)
			}
			_, err = ct.clients.src.GetConfigMap(ctx, ct.params.TestNamespace, frrConfigMapName, metav1.GetOptions{})
			if err != nil {
				cm := NewFRRConfigMap()
				ct.Logf("✨ [%s] Deploying %s configmap...", ct.clients.dst.ClusterName(), cm.Name)
				_, err = ct.clients.dst.CreateConfigMap(ctx, ct.params.TestNamespace, cm, metav1.CreateOptions{})
				if err != nil {
					return fmt.Errorf("unable to create configmap %s: %w", cm.Name, err)
				}
			}
		}
	}

	if ct.Features[features.Multicast].Enabled {
		_, err = ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, socatClientDeploymentName, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.src.ClusterName(), socatClientDeploymentName)
			ds := NewSocatClientDeployment(ct.params)
			_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(socatClientDeploymentName), metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create service account %s: %w", socatClientDeploymentName, err)
			}
			_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, ds, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create deployment %s: %w", socatClientDeploymentName, err)
			}
		}
	}

	if ct.Features[features.Multicast].Enabled {
		_, err = ct.clients.src.GetDaemonSet(ctx, ct.params.TestNamespace, socatServerDaemonsetName, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Deploying %s daemonset...", ct.clients.src.ClusterName(), socatServerDaemonsetName)
			ds := NewSocatServerDaemonSet(ct.params)
			_, err = ct.clients.src.CreateDaemonSet(ctx, ct.params.TestNamespace, ds, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create daemonset %s: %w", socatServerDaemonsetName, err)
			}
		}
	}

	return nil
}

func (ct *ConnectivityTest) createTestConnDisruptServerDeployAndSvc(ctx context.Context, deployName, kind string, replicas int, svcName, appLabel string,
	cnpFunc func(ns string) *ciliumv2.CiliumNetworkPolicy) error {
	_, err := ct.clients.src.GetDeployment(ctx, ct.params.TestNamespace, deployName, metav1.GetOptions{})
	if err != nil {
		ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.src.ClusterName(), deployName)
		readinessProbe := &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"cat", "/tmp/server-ready"},
				},
			},
			PeriodSeconds:       int32(3),
			InitialDelaySeconds: int32(1),
			FailureThreshold:    int32(20),
		}
		testConnDisruptServerDeployment := newDeployment(deploymentParameters{
			Name:           deployName,
			Kind:           kind,
			Image:          ct.params.TestConnDisruptImage,
			Replicas:       replicas,
			Labels:         map[string]string{"app": appLabel},
			Command:        []string{"tcd-server", "8000"},
			Port:           8000,
			ReadinessProbe: readinessProbe,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI)},
			},
		})
		_, err = ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(deployName), metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create service account %s: %w", deployName, err)
		}
		_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, testConnDisruptServerDeployment, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create deployment %s: %w", testConnDisruptServerDeployment, err)
		}
	}

	// Make sure that the server deployment is ready to spread client connections
	err = WaitForDeployment(ctx, ct, ct.clients.src, ct.params.TestNamespace, deployName)
	if err != nil {
		ct.Failf("%s deployment is not ready: %s", deployName, err)
	}

	for _, client := range ct.Clients() {
		_, err = client.GetService(ctx, ct.params.TestNamespace, svcName, metav1.GetOptions{})
		if err != nil {
			ct.Logf("✨ [%s] Deploying %s service...", client.ClusterName(), svcName)
			svc := newService(svcName, map[string]string{"app": appLabel}, nil, "http", 8000, ct.Params().ServiceType)
			svc.ObjectMeta.Annotations = map[string]string{"service.cilium.io/global": "true"}
			_, err = client.CreateService(ctx, ct.params.TestNamespace, svc, metav1.CreateOptions{})
			if err != nil {
				return fmt.Errorf("unable to create service %s: %w", svcName, err)
			}
		}

		if enabled, _ := ct.Features.MatchRequirements(features.RequireEnabled(features.CNP)); enabled {
			ipsec, _ := ct.Features.MatchRequirements(features.RequireMode(features.EncryptionPod, "ipsec"))
			if ipsec && versioncheck.MustCompile(">=1.14.0 <1.16.0")(ct.CiliumVersion) {
				// https://github.com/cilium/cilium/issues/36681
				continue
			}
			for _, client := range ct.Clients() {
				cnp := cnpFunc(ct.params.TestNamespace)
				ct.Logf("✨ [%s] Deploying %s CiliumNetworkPolicy...", client.ClusterName(), cnp.Name)
				_, err = client.ApplyGeneric(ctx, cnp)
				if err != nil {
					return fmt.Errorf("unable to create CiliumNetworkPolicy %s: %w", cnp.Name, err)
				}
			}
		}
	}

	return err
}

func (ct *ConnectivityTest) createTestConnDisruptClientDeployment(ctx context.Context, deployName, kind, appLabel, address string, replicas int, isExternal bool) error {
	_, err := ct.clients.dst.GetDeployment(ctx, ct.params.TestNamespace, deployName, metav1.GetOptions{})
	if err != nil {
		ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.dst.ClusterName(), deployName)
		readinessProbe := &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"cat", "/tmp/client-ready"},
				},
			},
			PeriodSeconds:       int32(3),
			InitialDelaySeconds: int32(1),
			FailureThreshold:    int32(20),
		}

		param := deploymentParameters{
			Name:     deployName,
			Kind:     kind,
			Image:    ct.params.TestConnDisruptImage,
			Replicas: replicas,
			Labels:   map[string]string{"app": appLabel},
			Command: []string{
				"tcd-client",
				"--dispatch-interval", ct.params.ConnDisruptDispatchInterval.String(),
				address,
			},
			ReadinessProbe: readinessProbe,
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: *resource.NewMilliQuantity(100, resource.DecimalSI)},
			},
		}
		if isExternal {
			param.NodeSelector = map[string]string{defaults.CiliumNoScheduleLabel: "true"}
			param.HostNetwork = true
			param.Tolerations = []corev1.Toleration{
				{Operator: corev1.TolerationOpExists},
			}
		}
		testConnDisruptClientDeployment := newDeployment(param)

		_, err = ct.clients.dst.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(deployName), metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create service account %s: %w", deployName, err)
		}
		_, err = ct.clients.dst.CreateDeployment(ctx, ct.params.TestNamespace, testConnDisruptClientDeployment, metav1.CreateOptions{})
		if err != nil {
			return fmt.Errorf("unable to create deployment %s: %w", testConnDisruptClientDeployment, err)
		}
	}

	return err
}

func (ct *ConnectivityTest) createTestConnDisruptClientDeploymentForNSTraffic(ctx context.Context) error {
	nodes, err := ct.getBackendNodeAndNonBackendNode(ctx)
	if err != nil {
		return err
	}

	for _, n := range nodes {
		for _, client := range ct.Clients() {
			svc, err := client.GetService(ctx, ct.params.TestNamespace, testConnDisruptNSTrafficServiceName, metav1.GetOptions{})
			if err != nil {
				return fmt.Errorf("unable to get service %s: %w", testConnDisruptNSTrafficServiceName, err)
			}

			var errs error
			np := uint16(svc.Spec.Ports[0].NodePort)
			addrs := slices.Clone(n.node.Status.Addresses)
			hasNetworkPolicies, err := ct.hasNetworkPolicies(ctx)
			if err != nil {
				return fmt.Errorf("failed to check if any netpol exists: %w", err)
			}
			ct.ForEachIPFamily(hasNetworkPolicies, func(family features.IPFamily) {
				for _, addr := range addrs {
					if features.GetIPFamily(addr.Address) != family {
						continue
					}

					// On GKE ExternalIP is not reachable from inside a cluster
					if addr.Type == corev1.NodeExternalIP {
						if f, ok := ct.Feature(features.Flavor); ok && f.Enabled && f.Mode == "gke" {
							continue
						}
					}

					deployName := fmt.Sprintf("%s-%s-%s-%s", testConnDisruptClientNSTrafficDeploymentName, n.nodeType, family, strings.ToLower(string(addr.Type)))
					if err := ct.createTestConnDisruptClientDeployment(ctx,
						deployName,
						KindTestConnDisruptNSTraffic,
						fmt.Sprintf("test-conn-disrupt-client-%s-%s-%s", n.nodeType, family, strings.ToLower(string(addr.Type))),
						netip.AddrPortFrom(netip.MustParseAddr(addr.Address), np).String(),
						1, true); err != nil {
						errs = errors.Join(errs, err)
					}
					ct.testConnDisruptClientNSTrafficDeploymentNames = append(ct.testConnDisruptClientNSTrafficDeploymentNames, deployName)
				}
			})
			if errs != nil {
				return errs
			}
		}
	}

	return err
}

type nodeWithType struct {
	nodeType string
	node     *corev1.Node
}

func (ct *ConnectivityTest) getBackendNodeAndNonBackendNode(ctx context.Context) ([]nodeWithType, error) {
	appLabel := fmt.Sprintf("app=%s", testConnDisruptServerNSTrafficAppLabel)
	podList, err := ct.clients.src.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: appLabel})
	if err != nil {
		return nil, fmt.Errorf("unable to list pods with lable %s: %w", appLabel, err)
	}

	pod := podList.Items[0]

	var nodes []nodeWithType
	nodes = append(nodes, nodeWithType{
		nodeType: "backend-node",
		node:     ct.nodes[pod.Spec.NodeName],
	})
	for name, node := range ct.Nodes() {
		if name != pod.Spec.NodeName {
			nodes = append(nodes, nodeWithType{
				nodeType: "non-backend-node",
				node:     node,
			})
			break
		}
	}

	return nodes, err
}

func (ct *ConnectivityTest) hasNetworkPolicies(ctx context.Context) (bool, error) {
	for _, client := range ct.Clients() {
		cnps, err := client.ListCiliumNetworkPolicies(ctx, ct.params.TestNamespace, metav1.ListOptions{Limit: 1})
		if err != nil {
			return false, err
		}
		if len(cnps.Items) > 0 {
			return true, nil
		}

		ccnps, err := client.ListCiliumClusterwideNetworkPolicies(ctx, metav1.ListOptions{Limit: 1})
		if err != nil {
			return false, err
		}
		if len(ccnps.Items) > 0 {
			return true, nil
		}

		nps, err := client.ListNetworkPolicies(ctx, metav1.ListOptions{Limit: 1})
		if err != nil {
			return false, err
		}
		if len(nps.Items) > 0 {
			return true, nil
		}
	}

	return false, nil
}

func (ct *ConnectivityTest) createClientPerfDeployment(ctx context.Context, name string, nodeName string, hostNetwork bool) error {
	ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.src.ClusterName(), name)
	gracePeriod := int64(1)
	perfClientDeployment := newDeployment(deploymentParameters{
		Name:  name,
		Kind:  kindPerfName,
		Image: ct.params.PerfParameters.Image,
		Labels: map[string]string{
			"client": "role",
		},
		Annotations:                   ct.params.DeploymentAnnotations.Match(name),
		Command:                       []string{"/bin/bash", "-c", "sleep 10000000"},
		NodeSelector:                  map[string]string{"kubernetes.io/hostname": nodeName},
		HostNetwork:                   hostNetwork,
		TerminationGracePeriodSeconds: &gracePeriod,
	})
	_, err := ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(name), metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("unable to create service account %s: %w", name, err)
	}
	_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, perfClientDeployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("unable to create deployment %s: %w", perfClientDeployment, err)
	}
	return nil
}

func (ct *ConnectivityTest) createServerPerfDeployment(ctx context.Context, name, nodeName string, hostNetwork bool) error {
	ct.Logf("✨ [%s] Deploying %s deployment...", ct.clients.src.ClusterName(), name)
	gracePeriod := int64(1)
	perfServerDeployment := newDeployment(deploymentParameters{
		Name: name,
		Kind: kindPerfName,
		Labels: map[string]string{
			"server": "role",
		},
		Annotations:                   ct.params.DeploymentAnnotations.Match(name),
		Port:                          5001,
		Image:                         ct.params.PerfParameters.Image,
		Command:                       []string{"/bin/bash", "-c", "netserver;sleep 10000000"},
		NodeSelector:                  map[string]string{"kubernetes.io/hostname": nodeName},
		HostNetwork:                   hostNetwork,
		TerminationGracePeriodSeconds: &gracePeriod,
	})
	_, err := ct.clients.src.CreateServiceAccount(ctx, ct.params.TestNamespace, k8s.NewServiceAccount(name), metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("unable to create service account %s: %w", name, err)
	}

	_, err = ct.clients.src.CreateDeployment(ctx, ct.params.TestNamespace, perfServerDeployment, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("unable to create deployment %s: %w", perfServerDeployment, err)
	}
	return nil
}

func (ct *ConnectivityTest) deployPerf(ctx context.Context) error {
	var err error

	nodeSelector := labels.SelectorFromSet(ct.params.NodeSelector).String()
	nodes, err := ct.client.ListNodes(ctx, metav1.ListOptions{LabelSelector: nodeSelector})
	if err != nil {
		return fmt.Errorf("unable to query nodes")
	}

	if len(nodes.Items) < 2 {
		return fmt.Errorf("Insufficient number of nodes selected with nodeSelector: %s", nodeSelector)
	}
	firstNodeName := nodes.Items[0].Name
	firstNodeZone := nodes.Items[0].Labels[corev1.LabelTopologyZone]
	secondNodeName := nodes.Items[1].Name
	secondNodeZone := nodes.Items[1].Labels[corev1.LabelTopologyZone]
	ct.Info("Nodes used for performance testing:")
	ct.Infof("Node name: %s, zone: %s", firstNodeName, firstNodeZone)
	ct.Infof("Node name: %s, zone: %s", secondNodeName, secondNodeZone)
	if firstNodeZone != secondNodeZone {
		ct.Warn("Selected nodes have different zones, tweak nodeSelector if that's not what you intended")
	}

	if ct.params.PerfParameters.PodNet {
		if ct.params.PerfParameters.NetQos {
			// Disable host net deploys
			ct.params.PerfParameters.HostNet = false
			// TODO: Merge with existing annotations
			var lowPrioDeployAnnotations = annotations{bwPrioAnnotationString: "5"}
			var highPrioDeployAnnotations = annotations{bwPrioAnnotationString: "6"}

			ct.params.DeploymentAnnotations.Set(`{
				"` + perClientLowPriorityDeploymentName + `": ` + lowPrioDeployAnnotations.String() + `,
			    "` + perClientHighPriorityDeploymentName + `": ` + highPrioDeployAnnotations.String() + `
			}`)
			if err = ct.createServerPerfDeployment(ctx, perfServerDeploymentName, firstNodeName, false); err != nil {
				ct.Warnf("unable to create deployment: %w", err)
			}
			// Create low priority client on other node
			if err = ct.createClientPerfDeployment(ctx, perClientLowPriorityDeploymentName, secondNodeName, false); err != nil {
				ct.Warnf("unable to create deployment: %w", err)
			}
			// Create high priority client on other node
			if err = ct.createClientPerfDeployment(ctx, perClientHighPriorityDeploymentName, secondNodeName, false); err != nil {
				ct.Warnf("unable to create deployment: %w", err)
			}
		} else {
			if err = ct.createClientPerfDeployment(ctx, perfClientDeploymentName, firstNodeName, false); err != nil {
				ct.Warnf("unable to create deployment: %w", err)
			}
			// Create second client on other node
			if err = ct.createClientPerfDeployment(ctx, perfClientAcrossDeploymentName, secondNodeName, false); err != nil {
				ct.Warnf("unable to create deployment: %w", err)
			}
			if err = ct.createServerPerfDeployment(ctx, perfServerDeploymentName, firstNodeName, false); err != nil {
				ct.Warnf("unable to create deployment: %w", err)
			}
		}
	}

	if ct.params.PerfParameters.HostNet {
		if err = ct.createClientPerfDeployment(ctx, perfClientHostNetDeploymentName, firstNodeName, true); err != nil {
			ct.Warnf("unable to create deployment: %w", err)
		}
		// Create second client on other node
		if err = ct.createClientPerfDeployment(ctx, perfClientHostNetAcrossDeploymentName, secondNodeName, true); err != nil {
			ct.Warnf("unable to create deployment: %w", err)
		}
		if err = ct.createServerPerfDeployment(ctx, perfServerHostNetDeploymentName, firstNodeName, true); err != nil {
			ct.Warnf("unable to create deployment: %w", err)
		}
	}

	return nil
}

// deploymentList returns 2 lists of Deployments to be used for running tests with.
func (ct *ConnectivityTest) deploymentList() (srcList []string, dstList []string) {
	if ct.params.Perf && ct.params.TestNamespaceIndex == 0 {
		if ct.params.PerfParameters.PodNet {
			if ct.params.PerfParameters.NetQos {
				srcList = append(srcList, perClientLowPriorityDeploymentName)
				srcList = append(srcList, perClientHighPriorityDeploymentName)
				srcList = append(srcList, perfServerDeploymentName)
			} else {
				srcList = append(srcList, perfClientDeploymentName)
				srcList = append(srcList, perfClientAcrossDeploymentName)
				srcList = append(srcList, perfServerDeploymentName)
			}
		}
		if ct.params.PerfParameters.HostNet {
			srcList = append(srcList, perfClientHostNetDeploymentName)
			srcList = append(srcList, perfClientHostNetAcrossDeploymentName)
			srcList = append(srcList, perfServerHostNetDeploymentName)
		}
		// Return early, we can't run regular connectivity tests
		// along perf test
		return
	}

	srcList = []string{clientDeploymentName, client2DeploymentName, echoSameNodeDeploymentName}
	if ct.params.MultiCluster == "" && !ct.params.SingleNode {
		srcList = append(srcList, client3DeploymentName)
	}

	if ct.params.IncludeConnDisruptTest && ct.params.TestNamespaceIndex == 0 {
		// We append the server and client deployment names to two different
		// lists. This matters when running in multi-cluster mode, because
		// the server is deployed in the local cluster (targeted by the "src"
		// client), while the client in the remote one (targeted by the "dst"
		// client). When running against a single cluster, instead, this does
		// not matter much, because the two clients are identical.
		srcList = append(srcList, testConnDisruptServerDeploymentName)
		dstList = append(dstList, testConnDisruptClientDeploymentName)
		if ct.ShouldRunConnDisruptNSTraffic() {
			srcList = append(srcList, testConnDisruptServerNSTrafficDeploymentName)
			dstList = append(dstList, ct.testConnDisruptClientNSTrafficDeploymentNames...)
		}
	}

	if ct.params.MultiCluster != "" || !ct.params.SingleNode {
		dstList = append(dstList, echoOtherNodeDeploymentName)
	}

	if ct.Features[features.NodeWithoutCilium].Enabled {
		srcList = append(srcList, echoExternalNodeDeploymentName)
	}

	if ct.Features[features.LocalRedirectPolicy].Enabled {
		srcList = append(srcList, lrpClientDeploymentName)
		srcList = append(srcList, lrpBackendDeploymentName)
	}

	if ct.Features[features.Multicast].Enabled {
		srcList = append(srcList, socatClientDeploymentName)
	}

	return srcList, dstList
}

func (ct *ConnectivityTest) deleteDeployments(ctx context.Context, client *k8s.Client) error {
	ct.Logf("🔥 [%s] Deleting connectivity check deployments...", client.ClusterName())
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, echoSameNodeDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, echoOtherNodeDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, clientDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, client2DeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, client3DeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, socatClientDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, socatServerDaemonsetName, metav1.DeleteOptions{}) // Q:Daemonset in here is OK?
	_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, echoSameNodeDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, echoOtherNodeDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, clientDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, client2DeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, client3DeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteService(ctx, ct.params.TestNamespace, echoSameNodeDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteService(ctx, ct.params.TestNamespace, echoOtherNodeDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteService(ctx, ct.params.TestNamespace, EchoOtherNodeDeploymentHeadlessServiceName, metav1.DeleteOptions{})
	_ = client.DeleteConfigMap(ctx, ct.params.TestNamespace, corednsConfigMapName, metav1.DeleteOptions{})
	_ = client.DeleteNamespace(ctx, ct.params.TestNamespace, metav1.DeleteOptions{})

	_, err := client.GetNamespace(ctx, ct.params.TestNamespace, metav1.GetOptions{})
	if err == nil {
		ct.Logf("⌛ [%s] Waiting for namespace %s to disappear", client.ClusterName(), ct.params.TestNamespace)
		for err == nil {
			time.Sleep(time.Second)
			// Retry the namespace deletion in-case the previous delete was
			// rejected, i.e. by yahoo/k8s-namespace-guard
			_ = client.DeleteNamespace(ctx, ct.params.TestNamespace, metav1.DeleteOptions{})
			_, err = client.GetNamespace(ctx, ct.params.TestNamespace, metav1.GetOptions{})
		}
	}

	return nil
}

func (ct *ConnectivityTest) DeleteConnDisruptTestDeployment(ctx context.Context, client *k8s.Client) error {
	ct.Debugf("🔥 [%s] Deleting test-conn-disrupt deployments...", client.ClusterName())
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, testConnDisruptClientDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, testConnDisruptServerDeploymentName, metav1.DeleteOptions{})
	deployList, err := client.ListDeployment(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "kind=" + KindTestConnDisruptNSTraffic})
	if err != nil {
		ct.Warnf("failed to list deployments: %s %v", KindTestConnDisruptNSTraffic, err)
	}
	for _, deploy := range deployList.Items {
		_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, deploy.Name, metav1.DeleteOptions{})
		_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, deploy.Name, metav1.DeleteOptions{})
	}
	_ = client.DeleteDeployment(ctx, ct.params.TestNamespace, testConnDisruptServerNSTrafficDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, testConnDisruptClientDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, testConnDisruptServerDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteServiceAccount(ctx, ct.params.TestNamespace, testConnDisruptServerNSTrafficDeploymentName, metav1.DeleteOptions{})
	_ = client.DeleteService(ctx, ct.params.TestNamespace, testConnDisruptServiceName, metav1.DeleteOptions{})
	_ = client.DeleteService(ctx, ct.params.TestNamespace, testConnDisruptNSTrafficServiceName, metav1.DeleteOptions{})
	_ = client.DeleteCiliumNetworkPolicy(ctx, ct.params.TestNamespace, testConnDisruptCNPName, metav1.DeleteOptions{})
	_ = client.DeleteCiliumNetworkPolicy(ctx, ct.params.TestNamespace, testConnDisruptNSTrafficCNPName, metav1.DeleteOptions{})

	return nil
}

// validateDeployment checks if the Deployments we created have the expected Pods in them.
func (ct *ConnectivityTest) validateDeployment(ctx context.Context) error {

	ct.Debug("Validating Deployments...")

	srcDeployments, dstDeployments := ct.deploymentList()
	for _, name := range srcDeployments {
		if err := WaitForDeployment(ctx, ct, ct.clients.src, ct.Params().TestNamespace, name); err != nil {
			return err
		}
	}

	for _, name := range dstDeployments {
		if err := WaitForDeployment(ctx, ct, ct.clients.dst, ct.Params().TestNamespace, name); err != nil {
			return err
		}
	}

	if ct.params.Perf {
		perfPods, err := ct.client.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "kind=" + kindPerfName})
		if err != nil {
			return fmt.Errorf("unable to list perf pods: %w", err)
		}
		for _, perfPod := range perfPods.Items {
			_, hasLabel := perfPod.GetLabels()["server"]
			if hasLabel {
				ct.perfServerPod = append(ct.perfServerPod, Pod{
					K8sClient: ct.client,
					Pod:       perfPod.DeepCopy(),
					port:      5201,
				})
			} else {
				ct.perfClientPods = append(ct.perfClientPods, Pod{
					K8sClient: ct.client,
					Pod:       perfPod.DeepCopy(),
				})
			}
		}
		// Sort pods so results are always displayed in the same order in console
		sort.SliceStable(ct.perfServerPod, func(i, j int) bool {
			return ct.perfServerPod[i].Pod.Name < ct.perfServerPod[j].Pod.Name
		})
		sort.SliceStable(ct.perfClientPods, func(i, j int) bool {
			return ct.perfClientPods[i].Pod.Name < ct.perfClientPods[j].Pod.Name
		})
		return nil
	}

	if ct.Features[features.LocalRedirectPolicy].Enabled {
		lrpPods, err := ct.client.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "kind=" + kindLrpName})
		if err != nil {
			return fmt.Errorf("unable to list lrp pods: %w", err)
		}
		for _, lrpPod := range lrpPods.Items {
			if v, hasLabel := lrpPod.GetLabels()["lrp"]; hasLabel {
				if v == "backend" {
					ct.lrpBackendPods[lrpPod.Name] = Pod{
						K8sClient: ct.client,
						Pod:       lrpPod.DeepCopy(),
					}
				} else if v == "client" {
					ct.lrpClientPods[lrpPod.Name] = Pod{
						K8sClient: ct.client,
						Pod:       lrpPod.DeepCopy(),
					}
				}
			}
		}
	}

	clientPods, err := ct.client.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "kind=" + kindClientName})
	if err != nil {
		return fmt.Errorf("unable to list client pods: %w", err)
	}

	for _, pod := range clientPods.Items {
		if strings.Contains(pod.Name, clientCPDeployment) {
			ct.clientCPPods[pod.Name] = Pod{
				K8sClient: ct.client,
				Pod:       pod.DeepCopy(),
			}
		} else {
			ct.clientPods[pod.Name] = Pod{
				K8sClient: ct.client,
				Pod:       pod.DeepCopy(),
			}

		}
	}

	sameNodePods, err := ct.clients.src.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "name=" + echoSameNodeDeploymentName})
	if err != nil {
		return fmt.Errorf("unable to list same node pods: %w", err)
	}
	if len(sameNodePods.Items) != 1 {
		return fmt.Errorf("unexpected number of same node pods: %d", len(sameNodePods.Items))
	}
	sameNodePod := Pod{
		Pod: sameNodePods.Items[0].DeepCopy(),
	}

	for _, cp := range ct.clientPods {
		err := WaitForPodDNS(ctx, ct, cp, sameNodePod)
		if err != nil {
			return err
		}
	}

	if !ct.params.SingleNode || ct.params.MultiCluster != "" {
		otherNodePods, err := ct.clients.dst.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "name=" + echoOtherNodeDeploymentName})
		if err != nil {
			return fmt.Errorf("unable to list other node pods: %w", err)
		}
		if len(otherNodePods.Items) != 1 {
			return fmt.Errorf("unexpected number of other node pods: %d", len(otherNodePods.Items))
		}
		otherNodePod := Pod{
			Pod: otherNodePods.Items[0].DeepCopy(),
		}

		for _, cp := range ct.clientPods {
			if err := WaitForPodDNS(ctx, ct, cp, otherNodePod); err != nil {
				return err
			}
		}
	}

	if ct.Features[features.NodeWithoutCilium].Enabled {
		echoExternalNodePods, err := ct.clients.dst.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "name=" + echoExternalNodeDeploymentName})
		if err != nil {
			return fmt.Errorf("unable to list other node pods: %w", err)
		}

		for _, pod := range echoExternalNodePods.Items {
			ct.echoExternalPods[pod.Name] = Pod{
				K8sClient: ct.client,
				Pod:       pod.DeepCopy(),
				scheme:    "http",
				port:      uint32(ct.Params().ExternalDeploymentPort), // listen port of the echo server inside the container
			}
		}

		echoExternalServices, err := ct.clients.dst.ListServices(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "kind=" + kindEchoExternalNodeName})
		if err != nil {
			return fmt.Errorf("unable to list echo external services: %w", err)
		}

		for _, echoExternalService := range echoExternalServices.Items {
			ct.echoExternalServices[echoExternalService.Name] = Service{
				Service: echoExternalService.DeepCopy(),
			}
		}
	}

	if ct.Features[features.BGPControlPlane].Enabled && ct.Features[features.NodeWithoutCilium].Enabled && ct.params.TestConcurrency == 1 {
		if err := WaitForDaemonSet(ctx, ct, ct.clients.src, ct.Params().TestNamespace, frrDaemonSetNameName); err != nil {
			return err
		}
		frrPods, err := ct.clients.dst.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "name=" + frrDaemonSetNameName})
		if err != nil {
			return fmt.Errorf("unable to list FRR pods: %w", err)
		}
		for _, pod := range frrPods.Items {
			ct.frrPods = append(ct.frrPods, Pod{
				K8sClient: ct.client,
				Pod:       pod.DeepCopy(),
			})
		}
	}

	if ct.Features[features.Multicast].Enabled {
		// socat client pods
		socatCilentPods, err := ct.clients.src.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "name=" + socatClientDeploymentName})
		if err != nil {
			return fmt.Errorf("unable to list socat client pods: %w", err)
		}
		for _, pod := range socatCilentPods.Items {
			ct.socatClientPods = append(ct.socatClientPods, Pod{
				K8sClient: ct.client,
				Pod:       pod.DeepCopy(),
			})
		}

		// socat server pods
		if err := WaitForDaemonSet(ctx, ct, ct.clients.src, ct.Params().TestNamespace, socatServerDaemonsetName); err != nil {
			return err
		}
		socatServerPods, err := ct.clients.src.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "name=" + socatServerDaemonsetName})
		if err != nil {
			return fmt.Errorf("unable to list socat server pods: %w", err)
		}
		for _, pod := range socatServerPods.Items {
			ct.socatServerPods = append(ct.socatServerPods, Pod{
				K8sClient: ct.client,
				Pod:       pod.DeepCopy(),
			})
		}
	}

	for _, cp := range ct.clientPods {
		if err := WaitForCoreDNS(ctx, ct, cp); err != nil {
			return err
		}
	}
	for _, cpp := range ct.clientCPPods {
		if err := WaitForCoreDNS(ctx, ct, cpp); err != nil {
			return err
		}
	}
	for _, client := range ct.clients.clients() {
		echoPods, err := client.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "kind=" + kindEchoName})
		if err != nil {
			return fmt.Errorf("unable to list echo pods: %w", err)
		}
		for _, echoPod := range echoPods.Items {
			ct.echoPods[echoPod.Name] = Pod{
				K8sClient: client,
				Pod:       echoPod.DeepCopy(),
				scheme:    "http",
				port:      8080, // listen port of the echo server inside the container
			}
		}
	}

	for _, client := range ct.clients.clients() {
		echoServices, err := client.ListServices(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "kind=" + kindEchoName})
		if err != nil {
			return fmt.Errorf("unable to list echo services: %w", err)
		}

		for _, echoService := range echoServices.Items {
			if ct.params.MultiCluster != "" {
				if _, exists := ct.echoServices[echoService.Name]; exists {
					// ct.clients.clients() lists the client cluster first.
					// If we already have this service (for the client cluster), keep it
					// so that we can rely on the service's ClusterIP being valid for the
					// client pods.
					continue
				}
			}

			ct.echoServices[echoService.Name] = Service{
				Service: echoService.DeepCopy(),
			}
		}
	}

	for _, s := range ct.echoServices {
		client := ct.RandomClientPod()
		if client == nil {
			return fmt.Errorf("no client pod available")
		}

		if err := WaitForService(ctx, ct, *client, s); err != nil {
			return err
		}

		// Wait until the service is propagated to the cilium agents
		// running on the nodes hosting the client pods.
		nodes := make(map[string]struct{})
		for _, client := range ct.ClientPods() {
			nodes[client.NodeName()] = struct{}{}
		}

		for _, agent := range ct.CiliumPods() {
			if _, ok := nodes[agent.NodeName()]; ok {
				if err := WaitForServiceEndpoints(ctx, ct, agent, s, 1, ct.Features.IPFamilies()); err != nil {
					return err
				}
			}
		}
	}

	if ct.Features[features.IngressController].Enabled {
		for name := range ct.ingresses() {
			svcName := fmt.Sprintf("cilium-ingress-%s", name)
			svc, err := WaitForServiceRetrieval(ctx, ct, ct.client, ct.params.TestNamespace, svcName)
			if err != nil {
				return err
			}

			ct.ingressService[svcName] = svc
		}
	}

	if ct.params.MultiCluster == "" {
		client := ct.RandomClientPod()
		if client == nil {
			return fmt.Errorf("no client pod available")
		}

		for _, ciliumPod := range ct.ciliumPods {
			hostIP := ciliumPod.Pod.Status.HostIP
			for _, s := range ct.echoServices {
				if err := WaitForNodePorts(ctx, ct, *client, hostIP, s); err != nil {
					return err
				}
			}
		}
	}

	// The host-netns-non-cilium DaemonSet is created in the source cluster only, also in case of multi-cluster tests.
	if !ct.params.SingleNode || ct.params.MultiCluster != "" {
		if err := WaitForDaemonSet(ctx, ct, ct.clients.src, ct.Params().TestNamespace, hostNetNSDeploymentNameNonCilium); err != nil {
			return err
		}
	}

	for _, client := range ct.clients.clients() {
		if !ct.params.SingleNode || ct.params.MultiCluster != "" {
			if err := WaitForDaemonSet(ctx, ct, client, ct.Params().TestNamespace, hostNetNSDeploymentName); err != nil {
				return err
			}
		}
		hostNetNSPods, err := client.ListPods(ctx, ct.params.TestNamespace, metav1.ListOptions{LabelSelector: "kind=" + kindHostNetNS})
		if err != nil {
			return fmt.Errorf("unable to list host netns pods: %w", err)
		}

		for _, pod := range hostNetNSPods.Items {
			_, ok := ct.nodesWithoutCilium[pod.Spec.NodeName]
			p := Pod{
				K8sClient: client,
				Pod:       pod.DeepCopy(),
				Outside:   ok,
			}
			ct.hostNetNSPodsByNode[pod.Spec.NodeName] = p

			if iface := ct.params.SecondaryNetworkIface; iface != "" {
				if ct.Features[features.IPv4].Enabled {
					cmd := []string{"/bin/sh", "-c", fmt.Sprintf("ip -family inet -oneline address show dev %s scope global | awk '{print $4}' | cut -d/ -f1", iface)}
					addr, err := client.ExecInPod(ctx, pod.Namespace, pod.Name, "", cmd)
					if err != nil {
						return fmt.Errorf("failed to fetch secondary network ip addr: %w", err)
					}
					ct.secondaryNetworkNodeIPv4[pod.Spec.NodeName] = strings.TrimSuffix(addr.String(), "\n")
				}
				if ct.Features[features.IPv6].Enabled {
					cmd := []string{"/bin/sh", "-c", fmt.Sprintf("ip -family inet6 -oneline address show dev %s scope global | awk '{print $4}' | cut -d/ -f1", iface)}
					addr, err := client.ExecInPod(ctx, pod.Namespace, pod.Name, "", cmd)
					if err != nil {
						return fmt.Errorf("failed to fetch secondary network ip addr: %w", err)
					}
					ct.secondaryNetworkNodeIPv6[pod.Spec.NodeName] = strings.TrimSuffix(addr.String(), "\n")
				}
			}
		}
	}

	// TODO: unconditionally re-enable the IPCache check once
	// https://github.com/cilium/cilium-cli/issues/361 is resolved.
	if ct.params.SkipIPCacheCheck {
		ct.Infof("Skipping IPCache check")
	} else {
		pods := append(slices.Collect(maps.Values(ct.clientPods)), slices.Collect(maps.Values(ct.echoPods))...)
		// Set the timeout for all IP cache lookup retries
		for _, cp := range ct.ciliumPods {
			if err := WaitForIPCache(ctx, ct, cp, pods); err != nil {
				return err
			}
		}
	}

	return nil
}
