package domain

import "testing"

func TestSecretReferencesRejectPlainCollisionAndObservedAuthority(t *testing.T) {
	for _, change := range []func(*Component){
		func(c *Component) { c.Env = map[string]string{"TOKEN": "plain"} },
		func(c *Component) { c.Services = map[string]string{"TOKEN": "web"} },
		func(c *Component) { c.SecretRefs = map[string]string{"BAD KEY": "token-v1"} },
		func(c *Component) { c.SecretRefs = map[string]string{"TOKEN": "plaintext/secret"} },
		func(c *Component) { c.ResourceID = "native"; c.Management = "observe" },
	} {
		m := valid()
		m.Components[0].SecretRefs = map[string]string{"TOKEN": "token-v1"}
		change(&m.Components[0])
		if m.Validate() == nil {
			t.Fatal("invalid secret configuration accepted")
		}
	}
	m := valid()
	m.Components[0].SecretRefs = map[string]string{"TOKEN": "token-v1"}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
}
