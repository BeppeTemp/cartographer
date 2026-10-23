package provisioning

// approveAll stands for the local approvals of every KB artifact of m, bound to
// their hashes: tests of the install paths of hooks and executable files pass it
// as ApplyOptions.ApprovedMCP. The approval contract has its own tests (D1141).
func approveAll(m Manifest) map[string]string {
	out := map[string]string{}
	for _, a := range m.Artifacts {
		out[a.Source+"\x00"+ApprovalName(a)] = a.ContentHash
	}
	return out
}
