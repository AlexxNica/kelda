package scheduler

import (
	"testing"

	"github.com/NetSys/di/db"
)

func TestAffinity(t *testing.T) {
	testAffinity(t, db.LabelRule{
		Exclusive:  true,
		OtherLabel: "foo",
	}, "affinity:di.user.label.foo!=1")

	testAffinity(t, db.LabelRule{
		Exclusive:  false,
		OtherLabel: "foo",
	}, "affinity:di.user.label.foo==1")

	testAffinity(t, db.MachineRule{
		Exclusive: false,
		Attribute: "size",
		Value:     "m4.large",
	}, "affinity:di.system.label.size==m4.large")

	testAffinity(t, db.PortRule{
		Port: 80,
	}, "affinity:di.system.label.port.80!=1")
}

func testAffinity(t *testing.T, rule db.PlacementRule, exp string) {
	res := rule.AffinityStr()
	if res != exp {
		t.Errorf("Affinity rule generation for %s failed. Expected %s, got %s",
			rule, exp, res)
	}
}
