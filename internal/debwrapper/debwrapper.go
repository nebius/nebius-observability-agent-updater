package debwrapper

import (
	"errors"
	"os/exec"
	"strings"
)

type DebWrapper struct {
}

var (
	ErrDebNotFound = errors.New("deb not found")
)

func NewDebWrapper() *DebWrapper {
	return &DebWrapper{}
}

func (d *DebWrapper) GetDebVersion(name string) (string, error) {
	cmd := exec.Command("dpkg-query", "-W", "-f=${Version}", name)
	output, err := cmd.Output()

	if err != nil {
		if strings.Contains(err.Error(), "no packages found") {
			return "", ErrDebNotFound
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}
