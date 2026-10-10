package access

import "testing"

func TestPasswordEncodingAndVerification(t *testing.T) {
	a, e := Password("a sufficiently long password")
	if e != nil {
		t.Fatal(e)
	}
	b, _ := Password("a sufficiently long password")
	if a == b {
		t.Fatal("salt reused")
	}
	if !Matches(a, "a sufficiently long password") || Matches(a, "incorrect password") || Matches("broken", "a sufficiently long password") {
		t.Fatal("password verification failed")
	}
	if _, e = Password("short"); e == nil {
		t.Fatal("short password accepted")
	}
}
