package coolify

import "testing"

func TestResourceProjectsPrimaryNativeEndpoint(t *testing.T) {
	for _, test := range []struct{ name, domains, want string }{
		{"single", "https://app.example.test", "https://app.example.test"},
		{"multiple", " https://first.example.test , https://second.example.test ", "https://first.example.test"},
		{"invalid first", "not-a-url,https://valid.example.test/path", "https://valid.example.test/path"},
		{"credentials", "https://user:secret@example.test,https://public.example.test", "https://public.example.test"},
		{"unsafe scheme", "javascript:alert(1),ftp://example.test", ""},
		{"control characters", "https://bad.example\n.test,https://good.example.test", "https://good.example.test"},
		{"internal whitespace", "https://bad example.test", ""},
		{"missing", "", ""},
		{"native port", "http://127.0.0.1:8080/health", "http://127.0.0.1:8080/health"},
	} {
		t.Run(test.name, func(t *testing.T) {
			native := application{UUID: "native-id", FQDN: test.domains}
			projected := resource(native)
			if projected.URL != test.want || native.FQDN != test.domains {
				t.Fatalf("endpoint projection=%q, want=%q; native domains must remain unchanged", projected.URL, test.want)
			}
		})
	}
}
