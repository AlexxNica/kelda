package network

import (
	"fmt"
	"net"
	"sort"
	"testing"

	"github.com/quilt/quilt/db"
	"github.com/quilt/quilt/minion/ipdef"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMakeIPContext(t *testing.T) {
	t.Parallel()

	conn := db.New()
	conn.Txn(db.AllTables...).Run(func(view db.Database) error {
		// A container with an IP address.
		dbc := view.InsertContainer()
		dbc.IP = "10.0.0.2"
		dbc.StitchID = "1"
		view.Commit(dbc)

		// A container without an IP address.
		dbc = view.InsertContainer()
		dbc.StitchID = "2"
		view.Commit(dbc)

		// A label with an IP address.
		label := view.InsertLabel()
		label.Label = "yellow"
		label.IP = "10.0.0.3"
		view.Commit(label)

		// A label without an IP address.
		label = view.InsertLabel()
		label.Label = "blue"
		view.Commit(label)

		return nil
	})

	var ctx ipContext
	conn.Txn(db.AllTables...).Run(func(view db.Database) error {
		ctx = makeIPContext(view)
		return nil
	})

	assert.Equal(t, map[string]struct{}{
		"10.0.0.0": {},
		"10.0.0.1": {},
		"10.0.0.2": {},
		"10.0.0.3": {},
	}, ctx.reserved)

	assert.Equal(t, []db.Container{
		{ID: 2, StitchID: "2"},
	}, ctx.unassignedContainers)

	assert.Equal(t, []db.Label{
		{ID: 4, Label: "blue"},
	}, ctx.unassignedLabels)
}

func TestAllocateContainerIPs(t *testing.T) {
	t.Parallel()
	conn := db.New()

	conn.Txn(db.AllTables...).Run(func(view db.Database) error {
		dbc := view.InsertContainer()
		dbc.IP = "10.0.0.2"
		dbc.StitchID = "1"
		view.Commit(dbc)

		dbc = view.InsertContainer()
		dbc.StitchID = "2"
		view.Commit(dbc)

		ctx := ipContext{
			reserved:             map[string]struct{}{},
			unassignedContainers: []db.Container{dbc},
		}
		allocateContainerIPs(view, ctx)
		return nil
	})

	dbcs := conn.SelectFromContainer(nil)
	assert.Len(t, dbcs, 2)

	sort.Sort(db.ContainerSlice(dbcs))

	dbc := dbcs[0]
	dbc.ID = 0
	assert.Equal(t, db.Container{IP: "10.0.0.2", StitchID: "1"}, dbc)

	dbc = dbcs[1]
	assert.Equal(t, "2", dbc.StitchID)
	assert.True(t, ipdef.QuiltSubnet.Contains(net.ParseIP(dbc.IP)))
}

func TestAllocateLabelIPs(t *testing.T) {
	t.Parallel()
	conn := db.New()

	conn.Txn(db.AllTables...).Run(func(view db.Database) error {
		label := view.InsertLabel()
		label.Label = "yellow"
		view.Commit(label)

		ctx := ipContext{
			reserved:         map[string]struct{}{},
			unassignedLabels: []db.Label{label},
		}
		assert.NoError(t, allocateLabelIPs(view, ctx))
		return nil
	})

	labels := conn.SelectFromLabel(nil)
	assert.Len(t, labels, 1)
	labelIP := net.ParseIP(labels[0].IP)
	assert.True(t, ipdef.QuiltSubnet.Contains(labelIP))
}

func TestSyncLabelContainerIPs(t *testing.T) {
	t.Parallel()
	conn := db.New()

	conn.Txn(db.AllTables...).Run(func(view db.Database) error {
		dbc := view.InsertContainer()
		dbc.Labels = []string{"red", "blue"}
		dbc.StitchID = "1"
		dbc.IP = "1.1.1.1"
		view.Commit(dbc)

		dbc = view.InsertContainer()
		dbc.Labels = []string{"red"}
		dbc.StitchID = "2"
		dbc.IP = "2.2.2.2"
		view.Commit(dbc)

		label := view.InsertLabel()
		label.Label = "yellow"
		view.Commit(label)

		return nil
	})

	conn.Txn(db.AllTables...).Run(func(view db.Database) error {
		syncLabelContainerIPs(view)
		return nil
	})

	actual := conn.SelectFromLabel(nil)
	assert.Len(t, actual, 2)
	assert.Contains(t, actual, db.Label{
		ID:           5,
		Label:        "blue",
		ContainerIPs: []string{"1.1.1.1"},
	})
	assert.Contains(t, actual, db.Label{
		ID:           4,
		Label:        "red",
		ContainerIPs: []string{"1.1.1.1", "2.2.2.2"},
	})
}

func TestAllocate(t *testing.T) {
	t.Parallel()

	subnet := net.IPNet{
		IP:   net.IPv4(0xab, 0xcd, 0xe0, 0x00),
		Mask: net.CIDRMask(20, 32),
	}
	conflicts := map[string]struct{}{}
	ipSet := map[string]struct{}{}

	// Only 4k IPs, in 0xfffff000. Guaranteed a collision
	for i := 0; i < 5000; i++ {
		ip, err := allocateIP(ipSet, subnet)
		if err != nil {
			continue
		}

		if _, ok := conflicts[ip]; ok {
			t.Fatalf("IP Double allocation: 0x%x", ip)
		}

		require.True(t, subnet.Contains(net.ParseIP(ip)),
			fmt.Sprintf("\"%s\" is not in %s", ip, subnet))
		conflicts[ip] = struct{}{}
	}

	assert.Equal(t, len(conflicts), len(ipSet))
	if len(conflicts) < 2500 || len(conflicts) > 4096 {
		// If the code's working, this is possible but *extremely* unlikely.
		// Probably a bug.
		t.Errorf("Too few conflicts: %d", len(conflicts))
	}
}
