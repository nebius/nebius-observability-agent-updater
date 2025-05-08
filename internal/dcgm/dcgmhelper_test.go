package dcgm

import (
	"testing"
)

func TestGetGpuInfoStr(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantModel  string
		wantNumber int
		wantErr    bool
	}{
		{
			name: "Valid input with one GPU",
			input: `1 GPU found.
+--------+----------------------------------------------------------------------+
| GPU ID | Device Information                                                   |
+--------+----------------------------------------------------------------------+
| 0      | Name: NVIDIA H200                                                    |
|        | PCI Bus ID: 00000000:8D:00.0                                         |
|        | Device UUID: GPU-65f8c655-bdb7-3246-468c-994c27fbb392                |
+--------+----------------------------------------------------------------------+`,
			wantModel:  "NVIDIA H200",
			wantNumber: 1,
			wantErr:    false,
		},
		{
			name: "Valid input with multiple GPUs",
			input: `4 GPUs found.
+--------+----------------------------------------------------------------------+
| GPU ID | Device Information                                                   |
+--------+----------------------------------------------------------------------+
| 0      | Name: NVIDIA A100                                                    |
|        | PCI Bus ID: 00000000:3B:00.0                                         |
|        | Device UUID: GPU-a1b2c3d4-e5f6-7890-abcd-ef1234567890                |
+--------+----------------------------------------------------------------------+`,
			wantModel:  "NVIDIA A100",
			wantNumber: 4,
			wantErr:    false,
		},
		{
			name:       "Empty input",
			input:      "",
			wantModel:  "",
			wantNumber: 0,
			wantErr:    true,
		},
		{
			name: "No GPU found",
			input: `0 GPU found.
+--------+----------------------------------------------------------------------+
| GPU ID | Device Information                                                   |
+--------+----------------------------------------------------------------------+
+--------+----------------------------------------------------------------------+`,
			wantModel:  "",
			wantNumber: 0,
			wantErr:    true,
		},
		{
			name: "Missing Name field",
			input: `2 GPUs found.
+--------+----------------------------------------------------------------------+
| GPU ID | Device Information                                                   |
+--------+----------------------------------------------------------------------+
| 0      | PCI Bus ID: 00000000:3B:00.0                                         |
|        | Device UUID: GPU-a1b2c3d4-e5f6-7890-abcd-ef1234567890                |
+--------+----------------------------------------------------------------------+`,
			wantModel:  "",
			wantNumber: 2,
			wantErr:    true,
		},
	}

	helper := NewDcgmHelper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotModel, gotNumber, err := helper.getGPUInfoStr(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("getGpuInfoStr() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if gotModel != tt.wantModel {
				t.Errorf("getGpuInfoStr() gotModel = %v, want %v", gotModel, tt.wantModel)
			}
			if gotNumber != tt.wantNumber {
				t.Errorf("getGpuInfoStr() gotNumber = %v, want %v", gotNumber, tt.wantNumber)
			}
		})
	}
}

func TestGetDCGMHostengineVersion(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name: "Valid input",
			input: `Version : 3.3.9
Build ID : 45
Build Date : 2024-11-13
Build Type : Release
Commit ID : 9e2b5d2b8914d2571537f9f633e5a91986d4eecd
Branch Name : rel_dcgm_3_3

Hostengine build info:
Version : 3.3.7
Build ID : 26
Build Date : 2024-07-09
Build Type : Release`,
			want:    "3.3.7",
			wantErr: false,
		},
		{
			name: "Different version format",
			input: `Version : 3.4.1
Build ID : 45

Hostengine build info:
Version : 4.0.2
Build ID : 26`,
			want:    "4.0.2",
			wantErr: false,
		},
		{
			name:    "Empty input",
			input:   "",
			want:    "",
			wantErr: true,
		},
		{
			name: "Missing hostengine info",
			input: `Version : 3.3.9
Build ID : 45
Build Date : 2024-11-13
Build Type : Release`,
			want:    "",
			wantErr: true,
		},
		{
			name: "Missing version after hostengine info",
			input: `Version : 3.3.9
Build ID : 45

Hostengine build info:
Build ID : 26
Build Date : 2024-07-09`,
			want:    "",
			wantErr: true,
		},
	}

	helper := NewDcgmHelper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := helper.getDCGMHostengineVersion(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("getDcgmHostengineVersion() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("getDcgmHostengineVersion() = %v, want %v", got, tt.want)
			}
		})
	}
}
