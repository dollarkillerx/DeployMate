package systeminfo

import "testing"

func TestCollectReturnsCoreHostInformation(t *testing.T) {
	info, err := Collect("test-version")
	if err != nil {
		t.Fatal(err)
	}
	if info.Hostname == "" || info.OS == "" || info.Architecture == "" || info.CPUCores < 1 {
		t.Fatalf("incomplete info: %+v", info)
	}
	if info.AgentVersion != "test-version" {
		t.Fatalf("version=%q", info.AgentVersion)
	}
}
