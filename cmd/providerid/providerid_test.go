package providerid

import (
	"testing"
)

func TestExtractInstanceName(t *testing.T) {
	tests := []struct {
		name         string
		nodeName     string
		expectedName string
	}{
		{
			name:         "GCP internal DNS format",
			nodeName:     "np-test4-kmbw6-qqqc7.us-central1-a.c.cveiga-gcp-hcp-2.internal",
			expectedName: "np-test4-kmbw6-qqqc7",
		},
		{
			name:         "Short hostname format",
			nodeName:     "instance-name.us-west1-b.example.com",
			expectedName: "instance-name",
		},
		{
			name:         "Plain instance name",
			nodeName:     "my-instance",
			expectedName: "my-instance",
		},
		{
			name:         "Single dot",
			nodeName:     "instance.",
			expectedName: "instance",
		},
		{
			name:         "Empty first part",
			nodeName:     ".zone.domain",
			expectedName: ".zone.domain",
		},
		{
			name:         "Multiple dots",
			nodeName:     "instance.zone.sub1.sub2.domain.com",
			expectedName: "instance",
		},
		{
			name:         "Instance with hyphens and numbers",
			nodeName:     "worker-01-abc123.europe-west1-c.c.project.internal",
			expectedName: "worker-01-abc123",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractInstanceName(tt.nodeName)
			if result != tt.expectedName {
				t.Errorf("extractInstanceName(%q) = %q, want %q", tt.nodeName, result, tt.expectedName)
			}
		})
	}
}

func TestExtractZoneFromNodeName(t *testing.T) {
	tests := []struct {
		name         string
		nodeName     string
		expectedZone string
	}{
		{
			name:         "GCP internal DNS format with valid zone",
			nodeName:     "np-test4-kmbw6-qqqc7.us-central1-a.c.cveiga-gcp-hcp-2.internal",
			expectedZone: "us-central1-a",
		},
		{
			name:         "Short hostname with valid zone",
			nodeName:     "instance.europe-west1-b.example.com",
			expectedZone: "europe-west1-b",
		},
		{
			name:         "Zone with different region format",
			nodeName:     "worker.asia-northeast1-c.domain",
			expectedZone: "asia-northeast1-c",
		},
		{
			name:         "No zone - plain instance name",
			nodeName:     "my-instance",
			expectedZone: "",
		},
		{
			name:         "No zone - invalid second part (no hyphens)",
			nodeName:     "instance.com.domain",
			expectedZone: "",
		},
		{
			name:         "No zone - too short second part",
			nodeName:     "instance.us.domain",
			expectedZone: "",
		},
		{
			name:         "No zone - empty second part",
			nodeName:     "instance..domain",
			expectedZone: "",
		},
		{
			name:         "Valid zone with numbers",
			nodeName:     "instance.us-west2-a.c.project.internal",
			expectedZone: "us-west2-a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractZoneFromNodeName(tt.nodeName)
			if result != tt.expectedZone {
				t.Errorf("extractZoneFromNodeName(%q) = %q, want %q", tt.nodeName, result, tt.expectedZone)
			}
		})
	}
}

func TestValidateInstanceName(t *testing.T) {
	tests := []struct {
		name        string
		instance    string
		expectError bool
	}{
		{
			name:        "Valid instance name - lowercase with hyphens",
			instance:    "my-instance-name",
			expectError: false,
		},
		{
			name:        "Valid instance name - lowercase with numbers",
			instance:    "worker-01-abc123",
			expectError: false,
		},
		{
			name:        "Valid instance name - single character",
			instance:    "a",
			expectError: false,
		},
		{
			name:        "Valid instance name - starts with letter ends with number",
			instance:    "instance-123",
			expectError: false,
		},
		{
			name:        "Valid instance name - numeric only (valid per GCP regex)",
			instance:    "123456789",
			expectError: false,
		},
		{
			name:        "Valid instance name - maximum length (63 chars)",
			instance:    "a234567890123456789012345678901234567890123456789012345678901",
			expectError: false,
		},
		{
			name:        "Invalid - empty string",
			instance:    "",
			expectError: true,
		},
		{
			name:        "Invalid - uppercase letters",
			instance:    "My-Instance",
			expectError: true,
		},
		{
			name:        "Invalid - contains dots",
			instance:    "instance.name",
			expectError: true,
		},
		{
			name:        "Invalid - contains underscores",
			instance:    "instance_name",
			expectError: true,
		},
		{
			name:        "Invalid - starts with hyphen",
			instance:    "-instance",
			expectError: true,
		},
		{
			name:        "Invalid - ends with hyphen",
			instance:    "instance-",
			expectError: true,
		},
		{
			name:        "Invalid - too long (64 chars)",
			instance:    "a234567890123456789012345678901234567890123456789012345678901234",
			expectError: true,
		},
		{
			name:        "Invalid - special characters",
			instance:    "instance@name",
			expectError: true,
		},
		{
			name:        "Invalid - starts with number 0",
			instance:    "0123456789",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateInstanceName(tt.instance)
			if tt.expectError && err == nil {
				t.Errorf("validateInstanceName(%q) expected error but got nil", tt.instance)
			}
			if !tt.expectError && err != nil {
				t.Errorf("validateInstanceName(%q) expected no error but got: %v", tt.instance, err)
			}
		})
	}
}
