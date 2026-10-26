package network

import "testing"

func TestPolicyRejectsAmbiguousDestinations(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "[::1]", "localhost", "*.example.org", "example.org.", "EXAMPLE.org", "a..b", "example.org:443", "-bad.example"} {
		if err := (Policy{TCP: []Destination{{Host: host, Port: 443}}}).Validate(); err == nil {
			t.Errorf("accepted %q", host)
		}
	}
	if err := (Policy{TCP: []Destination{{Host: "example.org", Port: 443}}}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (Policy{TCP: []Destination{{Host: "example.org", Port: 443}, {Host: "example.org", Port: 443}}}).Validate(); err == nil {
		t.Fatal("accepted duplicate")
	}
}

func TestParseDestination(t *testing.T) {
	destination, err := ParseDestination("example.org:443")
	if err != nil || destination != (Destination{Host: "example.org", Port: 443}) {
		t.Fatalf("destination=%+v err=%v", destination, err)
	}
	for _, input := range []string{"127.0.0.1:443", "example.org", "example.org:0", "*.example.org:443"} {
		if _, err := ParseDestination(input); err == nil {
			t.Errorf("accepted %q", input)
		}
	}
}
