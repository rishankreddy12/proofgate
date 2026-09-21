package secrets

import (
	"fmt"
	"os"
)

// FromConfig builds the configured KEK.
func FromConfig(kind, localFile, vaultAddr, vaultKey, vaultAuth, vaultRole string) (KEK, error) {
	switch kind {
	case "local":
		return LoadLocalKEK(localFile)
	case "vault":
		var ts TokenSource
		switch vaultAuth {
		case "kubernetes":
			ts = KubernetesAuth(vaultAddr, vaultRole, "/var/run/secrets/kubernetes.io/serviceaccount/token", nil)
		default:
			ts = StaticToken(os.Getenv("VAULT_TOKEN"))
		}
		return NewVaultKEK(vaultAddr, vaultKey, ts, nil), nil
	}
	return nil, fmt.Errorf("unknown kek %q", kind)
}
