package appdefinition

import (
	"fmt"
	v1 "github.com/acorn-io/acorn/pkg/apis/internal.acorn.io/v1"
	"github.com/acorn-io/acorn/pkg/config"
	"github.com/acorn-io/acorn/pkg/labels"
	"github.com/acorn-io/baaah/pkg/router"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NetworkPolicyForApp(req router.Request, resp router.Response) error {
	cfg, err := config.Get(req.Ctx, req.Client)
	if err != nil {
		return err
	} else if *cfg.DisableNetworkPolicies {
		return nil
	}

	app := req.Object.(*v1.AppInstance)
	appNamespace := app.Namespace        // this is where the AppInstance lives
	podNamespace := app.Status.Namespace // this is where the app is actually running

	// create the NetworkPolicy for the whole app
	// this allows traffic only from within the project
	resp.Objects(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      app.Name,
			Namespace: podNamespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: labels.Managed(app),
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{{
					NamespaceSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							labels.AcornAppNamespace: appNamespace,
						},
					}},
				},
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	})
	return nil
}

func NetworkPolicyForIngress(req router.Request, resp router.Response) error {
	cfg, err := config.Get(req.Ctx, req.Client)
	if err != nil {
		return err
	} else if *cfg.DisableNetworkPolicies {
		return nil
	}

	ingress := req.Object.(*networkingv1.Ingress)
	appName := ingress.Labels[labels.AcornAppName]
	projectName := ingress.Labels[labels.AcornAppNamespace]

	// create a mapping of k8s Service names to published port names/numbers
	svcNameToPorts := make(map[string][]networkingv1.ServiceBackendPort)
	for _, rule := range ingress.Spec.Rules {
		for _, path := range rule.HTTP.Paths {
			svcName := path.Backend.Service.Name
			port := path.Backend.Service.Port
			svcNameToPorts[svcName] = append(svcNameToPorts[svcName], port)
		}
	}

	for svcName, ports := range svcNameToPorts {
		// get the Service from k8s
		svc := corev1.Service{}
		err = req.Get(&svc, ingress.Namespace, svcName)
		if err != nil {
			return err
		}

		acornServiceName := svc.Labels[labels.AcornServiceName]

		// build the namespaceSelector for the NetPol
		var namespaceSelector metav1.LabelSelector
		if *cfg.IngressControllerNamespace != "" {
			namespaceSelector = metav1.LabelSelector{
				MatchLabels: map[string]string{
					"kubernetes.io/metadata.name": *cfg.IngressControllerNamespace,
				},
			}
		}

		// build the port slice for the NetPol
		var netPolPorts []networkingv1.NetworkPolicyPort
		for _, port := range ports {
			// try to map this ingress port to a port on the service
			for _, svcPort := range svc.Spec.Ports {
				if (svcPort.Name != "" && svcPort.Name == port.Name) || svcPort.Port == port.Number {
					targetPort := svcPort.TargetPort
					netPolPorts = append(netPolPorts, networkingv1.NetworkPolicyPort{
						Protocol: &[]corev1.Protocol{corev1.ProtocolTCP}[0],
						Port:     &targetPort,
					})
				}
			}
		}

		if len(netPolPorts) == 0 {
			logrus.Warnf("found no matching ports between Ingress %s and Service %s in Namespace %s", ingress.Name, svcName, ingress.Namespace)
			continue
		}

		// build the NetPol
		resp.Objects(&networkingv1.NetworkPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      fmt.Sprintf("%s-%s-%s-%s-%s", projectName, appName, ingress.Name, svcName, acornServiceName),
				Namespace: ingress.Namespace,
			},
			Spec: networkingv1.NetworkPolicySpec{
				PodSelector: metav1.LabelSelector{
					MatchLabels: svc.Spec.Selector, // the NetPol will target the same pods that the service targets
				},
				Ingress: []networkingv1.NetworkPolicyIngressRule{{
					From: []networkingv1.NetworkPolicyPeer{
						{
							NamespaceSelector: &namespaceSelector,
						},
						{
							NamespaceSelector: &metav1.LabelSelector{
								MatchLabels: map[string]string{
									"kubernetes.io/metadata.name": "acorn-system",
								},
							},
						},
					},
					Ports: netPolPorts,
				}},
				PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
			},
		})
	}

	return nil
}

func NetworkPolicyForService(req router.Request, resp router.Response) error {
	cfg, err := config.Get(req.Ctx, req.Client)
	if err != nil {
		return err
	} else if *cfg.DisableNetworkPolicies {
		return nil
	}

	service := req.Object.(*corev1.Service)

	// we only care about LoadBalancer services that were created for published TCP/UDP ports
	if service.Spec.Type != corev1.ServiceTypeLoadBalancer {
		return nil
	}

	appName := service.Labels[labels.AcornAppName]
	projectName := service.Labels[labels.AcornAppNamespace]
	acornServiceName := service.Labels[labels.AcornServiceName]

	// build the ipBlock for the NetPol
	ipBlock := networkingv1.IPBlock{
		CIDR: "0.0.0.0/0",
	}
	for _, cidr := range cfg.PodCIDRs {
		if cidr != "" {
			ipBlock.Except = append(ipBlock.Except, cidr)
		}
	}

	// build the port slice for the NetPol
	var netPolPorts []networkingv1.NetworkPolicyPort
	for _, port := range service.Spec.Ports {
		proto := port.Protocol
		targetPort := port.TargetPort
		netPolPorts = append(netPolPorts, networkingv1.NetworkPolicyPort{
			Protocol: &proto,
			Port:     &targetPort,
		})
	}

	// build the NetPol
	resp.Objects(&networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%s-%s-%s-%s", projectName, appName, service.Name, acornServiceName),
			Namespace: service.Namespace,
		},
		Spec: networkingv1.NetworkPolicySpec{
			PodSelector: metav1.LabelSelector{
				MatchLabels: service.Spec.Selector, // the NetPol will target the same pods that the service targets
			},
			Ingress: []networkingv1.NetworkPolicyIngressRule{{
				From: []networkingv1.NetworkPolicyPeer{
					{
						IPBlock: &ipBlock,
					},
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "kube-system",
							},
						},
					},
					{
						NamespaceSelector: &metav1.LabelSelector{
							MatchLabels: map[string]string{
								"kubernetes.io/metadata.name": "acorn-system",
							},
						},
					},
				},
				Ports: netPolPorts,
			}},
			PolicyTypes: []networkingv1.PolicyType{networkingv1.PolicyTypeIngress},
		},
	})

	return nil
}
