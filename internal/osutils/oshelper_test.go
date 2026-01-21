package osutils

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGetDebPackageVersion(t *testing.T) {
	o := NewOsHelper()
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
			result, err := o.GetDebVersion(tc.packageName)

			if tc.expectError && err == nil {
				t.Errorf("Expected an error for package %s, but got none", tc.packageName)
			}

			if !tc.expectError && err != nil {
				t.Errorf("Unexpected error for package %s: %v", tc.packageName, err)
			}
			if tc.expectError && err != nil && !errors.Is(err, ErrDebNotFound) {
				t.Errorf("Expected error %v for package %s, but got %v", ErrDebNotFound, tc.packageName, err)
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

func TestGetDirectorySize(t *testing.T) {
	o := NewOsHelper()

	// Check if du is available
	_, err := exec.LookPath("du")
	if err != nil {
		t.Skip("du command not found, skipping test")
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test-dir-size-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create some test files with known content
	testFile1 := filepath.Join(tempDir, "file1.txt")
	testFile2 := filepath.Join(tempDir, "file2.txt")

	if err := os.WriteFile(testFile1, []byte("Hello World"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}
	if err := os.WriteFile(testFile2, []byte("Test Data"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create a subdirectory
	subDir := filepath.Join(tempDir, "subdir")
	if err := os.Mkdir(subDir, 0755); err != nil {
		t.Fatalf("Failed to create subdirectory: %v", err)
	}
	testFile3 := filepath.Join(subDir, "file3.txt")
	if err := os.WriteFile(testFile3, []byte("More data"), 0644); err != nil {
		t.Fatalf("Failed to create test file in subdirectory: %v", err)
	}

	testCases := []struct {
		name        string
		path        string
		expectError bool
		checkSize   bool
		expectZero  bool
	}{
		{
			name:        "Valid directory with files",
			path:        tempDir,
			expectError: false,
			checkSize:   true,
			expectZero:  false,
		},
		{
			name:        "Non-existent directory",
			path:        "/path/that/does/not/exist/at/all",
			expectError: false,
			checkSize:   false,
			expectZero:  true,
		},
		{
			name:        "Empty path",
			path:        "",
			expectError: true,
			checkSize:   false,
			expectZero:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			size, err := o.GetDirectorySize(tc.path)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error for path %s, but got none", tc.path)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for path %s: %v", tc.path, err)
				}
				if tc.checkSize && size <= 0 {
					t.Errorf("Expected positive size for path %s, but got %d", tc.path, size)
				}
				if tc.expectZero && size != 0 {
					t.Errorf("Expected zero size for non-existent path %s, but got %d", tc.path, size)
				}
			}
		})
	}
}

func TestGetMountpointSize(t *testing.T) {
	o := NewOsHelper()

	// Check if df is available
	_, err := exec.LookPath("df")
	if err != nil {
		t.Skip("df command not found, skipping test")
	}

	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "test-mountpoint-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)

	testCases := []struct {
		name        string
		path        string
		expectError bool
		checkSize   bool
		expectZero  bool
	}{
		{
			name:        "Valid path (temp directory)",
			path:        tempDir,
			expectError: false,
			checkSize:   true,
			expectZero:  false,
		},
		{
			name:        "Root directory",
			path:        "/",
			expectError: false,
			checkSize:   true,
			expectZero:  false,
		},
		{
			name:        "Non-existent path",
			path:        "/path/that/does/not/exist/at/all",
			expectError: false,
			checkSize:   false,
			expectZero:  true,
		},
		{
			name:        "Empty path",
			path:        "",
			expectError: true,
			checkSize:   false,
			expectZero:  false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			size, err := o.GetMountpointSize(tc.path)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error for path %s, but got none", tc.path)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error for path %s: %v", tc.path, err)
				}
				if tc.checkSize && size <= 0 {
					t.Errorf("Expected positive size for path %s, but got %d", tc.path, size)
				}
				if tc.expectZero && size != 0 {
					t.Errorf("Expected zero size for non-existent path %s, but got %d", tc.path, size)
				}
			}
		})
	}
}
