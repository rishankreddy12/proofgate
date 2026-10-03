// Package secrets provides enterprise-grade capabilities, configuration, and structural components for the secrets subsystem.
package secrets

import (
	"fmt"
	"os"
)

// FromConfig builds the configured KEK.
func FromConfig(kind, localFile, vaultAddr, vaultKey, vaultAuth, vaultRole string) (KEK, error) {
	return FromConfigWithOptions(kind, localFile, vaultAddr, vaultKey, vaultAuth, vaultRole, "")
}

// FromConfigWithOptions builds the configured KEK with optional token file for Kubernetes auth.
func FromConfigWithOptions(kind, localFile, vaultAddr, vaultKey, vaultAuth, vaultRole, vaultTokenFile string) (KEK, error) {
	switch kind {
	case "local":
		return LoadLocalKEK(localFile)
	case "vault":
		var ts TokenSource
		switch vaultAuth {
		case "kubernetes":
			tokenPath := vaultTokenFile
			if tokenPath == "" {
				tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
			}
			ts = KubernetesAuth(vaultAddr, vaultRole, tokenPath, nil)
		default:
			ts = StaticToken(os.Getenv("VAULT_TOKEN"))
		}
		return NewVaultKEK(vaultAddr, vaultKey, ts, nil), nil
	}
	return nil, fmt.Errorf("unknown kek %q", kind)
}

// NewDynamicVaultResolverFromConfig creates a dynamic vault resolver from global vault configuration.
func NewDynamicVaultResolverFromConfig(vaultAddr, vaultAuth, vaultRole, vaultTokenFile string) *DynamicVaultResolver {
	var ts TokenSource
	switch vaultAuth {
	case "kubernetes":
		tokenPath := vaultTokenFile
		if tokenPath == "" {
			tokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
		}
		ts = KubernetesAuth(vaultAddr, vaultRole, tokenPath, nil)
	default:
		ts = StaticToken(os.Getenv("VAULT_TOKEN"))
	}
	return NewDynamicVaultResolver(vaultAddr, ts, nil)
}
