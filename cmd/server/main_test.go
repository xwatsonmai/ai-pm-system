package main

import "testing"

func TestListenAddressRequiresAuthForRemoteAccess(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8090", "[::1]:8090", "localhost:8090"} {
		if err := validateListenAddress(addr, false); err != nil {
			t.Errorf("local address %s: %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:8090", ":8090", "[::]:8090", "192.0.2.10:8090"} {
		if err := validateListenAddress(addr, false); err == nil {
			t.Errorf("unauthenticated remote address %s accepted", addr)
		}
		if err := validateListenAddress(addr, true); err != nil {
			t.Errorf("authenticated remote address %s: %v", addr, err)
		}
	}
	for _, addr := range []string{"127.0.0.1", "127.0.0.1:", "127.0.0.1:-1", "127.0.0.1:65536"} {
		if err := validateListenAddress(addr, true); err == nil {
			t.Errorf("invalid address %s accepted", addr)
		}
	}
}
