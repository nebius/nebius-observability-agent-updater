package dcgm

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type Helper struct {
}

func NewDcgmHelper() *Helper {
	return &Helper{}
}

func (h *Helper) GetDCGMVersion() (string, error) {
	cmd := exec.Command("dcgmi", "-v")
	output, err := cmd.Output()

	if err != nil {
		return "", fmt.Errorf("failed to get dcgm version: %w", err)
	}

	return h.getDCGMHostengineVersion(string(output))
}

func (h *Helper) GetGpuInfo() (model string, number int, err error) {
	cmd := exec.Command("dcgmi", "discovery", "-l")
	output, err := cmd.Output()

	if err != nil {
		return "", 0, fmt.Errorf("failed to get gpu info: %w", err)
	}

	return h.getGPUInfoStr(string(output))
}

func (h *Helper) getGPUInfoStr(output string) (model string, number int, err error) {
	/*
		 dcgmi discovery -l
		1 GPU found.
		+--------+----------------------------------------------------------------------+
		| GPU ID | Device Information                                                   |
		+--------+----------------------------------------------------------------------+
		| 0      | Name: NVIDIA H200                                                    |
		|        | PCI Bus ID: 00000000:8D:00.0                                         |
		|        | Device UUID: GPU-65f8c655-bdb7-3246-468c-994c27fbb392                |
		+--------+----------------------------------------------------------------------+
		0 NvSwitches found.
		+-----------+
		| Switch ID |
		+-----------+
		+-----------+
		0 CPUs found.
		+--------+----------------------------------------------------------------------+
		| CPU ID | Device Information                                                   |
		+--------+----------------------------------------------------------------------+
		+--------+----------------------------------------------------------------------+
	*/
	numRegex := regexp.MustCompile(`(\d+)\s+GPUs? found`)
	numMatches := numRegex.FindStringSubmatch(output)
	if len(numMatches) >= 2 {
		number, err = strconv.Atoi(numMatches[1])
		if err != nil {
			return "", 0, fmt.Errorf("failed to parse number of GPUs: %w", err)
		}
	}

	// Find the GPU model name using regex
	nameRegex := regexp.MustCompile(`Name:\s+([^|]+)`)
	nameMatches := nameRegex.FindStringSubmatch(output)
	if len(nameMatches) >= 2 {
		model = strings.TrimSpace(nameMatches[1])
	}

	// Handle error cases
	if number == 0 {
		return "", 0, fmt.Errorf("no GPUs found in output")
	}

	if model == "" {
		return "", number, fmt.Errorf("found %d GPUs but couldn't determine model name", number)
	}

	return model, number, nil
}

func (h *Helper) getDCGMHostengineVersion(output string) (string, error) {
	/*
		Version : 3.3.9
		Build ID : 45
		Build Date : 2024-11-13
		Build Type : Release
		Commit ID : 9e2b5d2b8914d2571537f9f633e5a91986d4eecd
		Branch Name : rel_dcgm_3_3
		CPU Arch : x86_64
		Build Platform : Linux 4.15.0-180-generic #189-Ubuntu SMP Wed May 18 14:13:57 UTC 2022 x86_64
		CRC : 813bd4bc82cddbb63b59936dc0740c84

		Hostengine build info:
		Version : 3.3.7
		Build ID : 26
		Build Date : 2024-07-09
		Build Type : Release
		Commit ID : 105620196e46a7ef2f99a1ce3e69a5d12af1e845
		Branch Name : rel_dcgm_3_3
		CPU Arch : x86_64
		Build Platform : Linux 4.15.0-180-generic #189-Ubuntu SMP Wed May 18 14:13:57 UTC 2022 x86_64
		CRC : c1b74febf52d45d29ae956b78c091857
	*/
	lines := strings.Split(output, "\n")
	foundHostengine := false

	for _, line := range lines {
		trimmedLine := strings.TrimSpace(line)

		if foundHostengine && strings.HasPrefix(trimmedLine, "Version") {
			parts := strings.Split(trimmedLine, ":")
			if len(parts) >= 2 {
				return strings.TrimSpace(parts[1]), nil
			}
			break
		}

		if trimmedLine == "Hostengine build info:" {
			foundHostengine = true
		}
	}
	return "", fmt.Errorf("failed to find hostengine version in output")
}
