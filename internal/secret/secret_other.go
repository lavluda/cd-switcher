//go:build !darwin

package secret

// sessionCredential is unimplemented outside macOS today (Windows DPAPI /
// Linux libsecret are future work — see docs/settings-and-stats-plan.md).
func sessionCredential(snapshotDir string) (Credential, error) {
	return Credential{}, ErrUnsupported
}
