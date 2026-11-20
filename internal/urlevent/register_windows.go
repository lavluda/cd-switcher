package urlevent

import "golang.org/x/sys/windows/registry"

// Register makes this executable the cdswitcher:// handler for the user,
// under HKCU so no admin rights are needed.
func Register(exe string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\cdswitcher`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if err := k.SetStringValue("", "URL:CD-Switcher link"); err != nil {
		return err
	}
	if err := k.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}
	cmd, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\Classes\cdswitcher\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer cmd.Close()
	return cmd.SetStringValue("", `"`+exe+`" "%1"`)
}
