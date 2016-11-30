package minion

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/NetSys/quilt/db"
	"github.com/NetSys/quilt/minion/pb"
)

func TestSetMinionConfig(t *testing.T) {
	t.Parallel()
	s := server{db.New()}

	cfg := pb.MinionConfig{
		Role:      pb.MinionConfig_MASTER,
		PrivateIP: "priv",
		Spec:      "spec",
		Provider:  "provider",
		Size:      "size",
		Region:    "region",
	}
	exp := db.Minion{
		Self:      true,
		Spec:      "spec",
		Role:      db.Master,
		PrivateIP: "priv",
		Provider:  "provider",
		Size:      "size",
		Region:    "region",
	}
	_, err := s.SetMinionConfig(nil, &cfg)
	assert.NoError(t, err)
	checkMinionEquals(t, s.Conn, exp)

	// Update a field.
	cfg.Spec = "new"
	exp.Spec = "new"
	_, err = s.SetMinionConfig(nil, &cfg)
	assert.NoError(t, err)
	checkMinionEquals(t, s.Conn, exp)
}

func checkMinionEquals(t *testing.T, conn db.Conn, exp db.Minion) {
	timeout := time.After(1 * time.Second)
	var actual db.Minion
	for {
		actual, _ = conn.MinionSelf()
		actual.ID = 0
		if reflect.DeepEqual(exp, actual) {
			return
		}
		select {
		case <-timeout:
			t.Errorf("Expected minion to be %v, but got %v\n", exp, actual)
			return
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}
}

func TestGetMinionConfig(t *testing.T) {
	t.Parallel()
	s := server{db.New()}

	// Should set Role to None if no config.
	cfg, err := s.GetMinionConfig(nil, &pb.Request{})
	assert.NoError(t, err)
	assert.Equal(t, pb.MinionConfig{Role: pb.MinionConfig_NONE}, *cfg)

	// Should only return config for "self".
	s.Conn.Transact(func(view db.Database) error {
		m := view.InsertMinion()
		m.Self = false
		m.Spec = "spec"
		m.Role = db.Master
		m.PrivateIP = "priv"
		m.Provider = "provider"
		m.Size = "size"
		m.Region = "region"
		view.Commit(m)
		return nil
	})
	cfg, err = s.GetMinionConfig(nil, &pb.Request{})
	assert.NoError(t, err)
	assert.Equal(t, pb.MinionConfig{Role: pb.MinionConfig_NONE}, *cfg)

	// Test returning a full config.
	s.Conn.Transact(func(view db.Database) error {
		m := view.SelectFromMinion(nil)[0]
		m.Self = true
		view.Commit(m)
		return nil
	})
	cfg, err = s.GetMinionConfig(nil, &pb.Request{})
	assert.NoError(t, err)
	assert.Equal(t, pb.MinionConfig{
		Role:      pb.MinionConfig_MASTER,
		PrivateIP: "priv",
		Spec:      "spec",
		Provider:  "provider",
		Size:      "size",
		Region:    "region",
	}, *cfg)
}
