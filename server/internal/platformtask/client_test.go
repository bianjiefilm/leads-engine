package platformtask

import "testing"

func TestNewRequiresAllThreeKeys(t *testing.T) {
	if New("http://127.0.0.1:9", "token", "acct", "leads-engine") == nil {
		t.Fatal("three keys must build a client")
	}
	if New("", "token", "acct", "leads-engine") != nil || New("http://127.0.0.1:9", "", "acct", "leads-engine") != nil || New("http://127.0.0.1:9", "token", "", "leads-engine") != nil {
		t.Fatal("a missing key must stay text-degraded")
	}
}
