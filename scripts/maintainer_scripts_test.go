package scripts

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const maintainerScriptsDir = "../debian/DEBIAN"

func TestMaintainerScriptsNeverReloadSystemd(t *testing.T) {
	for _, script := range []string{"postinst", "postrm"} {
		t.Run(script, func(t *testing.T) {
			content, err := os.ReadFile(filepath.Join(maintainerScriptsDir, script))
			require.NoError(t, err)

			for i, line := range strings.Split(string(content), "\n") {
				args := systemctlArgs(line)
				assert.NotContains(t, args, "daemon-reload",
					"%s:%d reloads systemd, see the comment in postinst", script, i+1)
				if slices.Contains(args, "enable") || slices.Contains(args, "disable") {
					assert.Contains(t, args, "--no-reload",
						"%s:%d reloads systemd through enable or disable, see the comment in postinst", script, i+1)
				}
			}
		})
	}
}

func systemctlArgs(line string) []string {
	fields := strings.Fields(line)
	for i, field := range fields {
		if isComment(field) {
			return nil
		}
		if strings.HasSuffix(field, "systemctl") {
			args := fields[i+1:]
			if end := slices.IndexFunc(args, isComment); end >= 0 {
				args = args[:end]
			}
			return args
		}
	}
	return nil
}

func isComment(field string) bool {
	return strings.HasPrefix(field, "#")
}
