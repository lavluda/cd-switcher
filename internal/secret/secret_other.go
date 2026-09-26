//go:build !darwin && !windows && !linux

package secret

func sessionCredential(snapshotDir string) (Credential, error) {
	return Credential{}, ErrUnsupported
}

func updateStoredToken(snapshotDir string, cred Credential) error {
	return ErrUnsupported
}
