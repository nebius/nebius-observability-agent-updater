package debwrapper

import (
	"errors"
	"os/exec"
	"testing"
)

func TestGetDebPackageVersion(t *testing.T) {
	// Check if dpkg is available
	_, err := exec.LookPath("dpkg-query")
	if err != nil {
		t.Skip("dpkg-query not found, skipping test")
	}

	// Test cases
	testCases := []struct {
		name           string
		packageName    string
		expectedResult string
		expectError    bool
	}{
		{
			name:           "Existing package",
			packageName:    "bash", // bash is likely to be installed on most Linux systems
			expectedResult: "",     // We don't know the exact version, but it should not be empty
			expectError:    false,
		},
		{
			name:           "Non-existent package",
			packageName:    "this-package-does-not-exist",
			expectedResult: "",
			expectError:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			debWrapper := NewDebWrapper()
			result, err := debWrapper.GetDebVersion(tc.packageName)

			if tc.expectError && err == nil {
				t.Errorf("Expected an error for package %s, but got none", tc.packageName)
			}

			if !tc.expectError && err != nil && !errors.Is(err, ErrDebNotFound) {
				t.Errorf("Unexpected error for package %s: %v", tc.packageName, err)
			}

			if !tc.expectError && result == "" {
				t.Errorf("Expected a non-empty version for package %s, but got an empty string", tc.packageName)
			}

			if tc.expectedResult != "" && result != tc.expectedResult {
				t.Errorf("Expected version %s for package %s, but got %s", tc.expectedResult, tc.packageName, result)
			}
		})
	}
}
