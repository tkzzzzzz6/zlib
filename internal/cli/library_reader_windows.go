package cli

import "golang.org/x/sys/windows"

func launchBookReader(path string) error {
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), file, nil, nil, windows.SW_SHOWNORMAL)
}
