package net

import (
	"context"
	"fmt"
	"net/http"

	"github.com/kommodity-io/kommodity/pkg/config"
	"k8s.io/apimachinery/pkg/labels"
	clusterv1 "sigs.k8s.io/cluster-api/api/v1beta1"
	ctrlclint "sigs.k8s.io/controller-runtime/pkg/client"
)

// GetOriginalIPFromRequest extracts the IP address from the HTTP request, X-Forwarded-For if present.
func GetOriginalIPFromRequest(request *http.Request) (string, error) {
	//nolint:varnamelen // Variable name ip is appropriate for the context.
	ip := request.Header.Get("X-Forwarded-For")
	if ip == "" {
		ip = request.RemoteAddr
		if ip == "" {
			return "", ErrIPRequired
		}
	}

	return ip, nil
}

// FindManagedMachineByIP finds a managed-by-kommodity Machine by its IP address.
//
//nolint:varnamelen // Variable name ip is appropriate for the context.
func FindManagedMachineByIP(ctx context.Context, ctrlClient *ctrlclint.Client, ip string) (*clusterv1.Machine, error) {
	var machines clusterv1.MachineList

	err := (*ctrlClient).List(ctx, &machines, &ctrlclint.ListOptions{
		LabelSelector: labels.SelectorFromSet(map[string]string{
			config.ManagedByLabel: "kommodity",
		}),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list managed-by kommodity machines: %w", err)
	}

	for _, machine := range machines.Items {
		addresses := machine.Status.Addresses
		if len(addresses) == 0 {
			continue
		}

		for _, addr := range addresses {
			if addr.Address == ip {
				return &machine, nil
			}
		}
	}

	return nil, ErrNoMachineFound
}
