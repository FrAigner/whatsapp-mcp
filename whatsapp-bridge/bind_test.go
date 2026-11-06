package main

import "testing"

func TestBridgeBindDefaultsToLoopback(t *testing.T) {
	t.Setenv("WHATSAPP_BRIDGE_BIND", "")
	if got := bridgeBindAddr(8080); got != "127.0.0.1:8080" {
		t.Fatalf("default bind must stay loopback, got %q", got)
	}
}

func TestBridgeBindCanBeOverriddenForContainers(t *testing.T) {
	t.Setenv("WHATSAPP_BRIDGE_BIND", "0.0.0.0")
	if got := bridgeBindAddr(8080); got != "0.0.0.0:8080" {
		t.Fatalf("got %q", got)
	}
}

func TestExtraAllowedHostsAreAddedButDefaultsRemain(t *testing.T) {
	t.Setenv("WHATSAPP_BRIDGE_ALLOWED_HOSTS", " Bridge:8080 , whatsapp-bridge:8080 ,")
	hosts := buildAllowedHosts(8080)
	for _, h := range []string{"bridge:8080", "whatsapp-bridge:8080", "127.0.0.1:8080", "localhost:8080"} {
		if _, ok := hosts[h]; !ok {
			t.Fatalf("missing allowed host %q", h)
		}
	}
	if _, ok := hosts["evil.example.com:8080"]; ok {
		t.Fatal("unexpected host allowed")
	}
}

func TestNoExtraHostsByDefault(t *testing.T) {
	t.Setenv("WHATSAPP_BRIDGE_ALLOWED_HOSTS", "")
	if n := len(buildAllowedHosts(8080)); n != 3 {
		t.Fatalf("expected only the 3 loopback hosts, got %d", n)
	}
}
