package cli

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Standalone commands share shell pages and transitions, then exit on return.
func newStandaloneShellCommand(command string) (shellModel, error) {
	m := newShellModel()
	m.standalone = true
	m.standaloneTitle = "Kindle"
	if strings.HasPrefix(command, "/weread") {
		m.standaloneTitle = "WeRead"
	}
	if strings.HasPrefix(command, "/meta") {
		m.standaloneTitle = "Metadata"
	}
	if strings.HasPrefix(command, "/library") {
		m.standaloneTitle = "Local bookshelf"
	}
	if strings.HasPrefix(command, "/preview") {
		m.standaloneTitle = "Book preview"
	}
	m.dispatch(command)
	if m.page == nil && m.kind == "" {
		return m, fmt.Errorf("%s", m.status)
	}
	return m, nil
}

func runStandaloneShellCommand(command string) error {
	if !isInteractive() {
		// Only the Kindle commands have a plain form to suggest.
		if strings.HasPrefix(command, "/kindle") {
			return fmt.Errorf("this command requires a terminal; use zlib kindle send <file> for non-interactive delivery")
		}
		return fmt.Errorf("this command requires a terminal")
	}
	m, err := newStandaloneShellCommand(command)
	if err != nil {
		return err
	}
	// The metadata and local book pages can draw images.
	var result tea.Model
	if strings.HasPrefix(command, "/meta") || strings.HasPrefix(command, "/library") || strings.HasPrefix(command, "/preview") {
		result, err = runCoverProgram(m)
	} else {
		result, err = tea.NewProgram(m).Run()
	}
	if err != nil {
		return err
	}
	return result.(shellModel).commandErr
}
