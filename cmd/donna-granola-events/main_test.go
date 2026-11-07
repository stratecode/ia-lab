package main

import "testing"

func TestListenAddressDefaultsToConfiguredDeploymentPort(t *testing.T) {
	if got := listenAddress(func(string) string { return "" }); got != "127.0.0.1:8085" {
		t.Fatalf("listenAddress() = %q, want deployment port 8085", got)
	}
	if got := listenAddress(func(string) string { return "127.0.0.1:9000" }); got != "127.0.0.1:9000" {
		t.Fatalf("listenAddress() ignored explicit override: %q", got)
	}
}
