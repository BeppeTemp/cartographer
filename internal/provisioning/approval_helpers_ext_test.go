package provisioning_test

import "github.com/BeppeTemp/cartographer/internal/provisioning"

// approveAll: see the helper of the same name in package provisioning.
func approveAll(m provisioning.Manifest) map[string]string {
	out := map[string]string{}
	for _, a := range m.Artifacts {
		out[a.Source+"\x00"+provisioning.ApprovalName(a)] = a.ContentHash
	}
	return out
}
