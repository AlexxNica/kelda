//go:generate protoc ./pb/pb.proto --go_out=plugins=grpc:.
package main

import (
	"github.com/NetSys/di/db"
	"github.com/NetSys/di/minion/consensus"
	"github.com/NetSys/di/minion/docker"
	"github.com/NetSys/di/minion/elector"
	"github.com/NetSys/di/minion/network"
	"github.com/NetSys/di/minion/scheduler"
	"github.com/NetSys/di/minion/supervisor"
	"github.com/NetSys/di/util"

	log "github.com/Sirupsen/logrus"
)

func main() {
	log.Info("Minion Start")

	log.SetFormatter(util.Formatter{})

	conn := db.New()
	dk := docker.New("unix:///var/run/docker.sock")
	go minionServerRun(conn)
	go supervisor.Run(conn, dk)
	go scheduler.Run(conn)

	store := consensus.NewStore()
	go elector.Run(conn, store)
	go network.Run(conn, store, dk)

	for range conn.TriggerTick(60, db.MinionTable).C {
		conn.Transact(func(view db.Database) error {
			minions := view.SelectFromMinion(nil)
			if len(minions) != 1 {
				return nil
			}
			updatePolicy(view, minions[0].Role, minions[0].Spec)
			return nil
		})
	}
}
