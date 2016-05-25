//go:generate protoc ./pb/pb.proto --go_out=plugins=grpc:.
package main

import (
	"time"

	"github.com/NetSys/quilt/db"
	"github.com/NetSys/quilt/minion/consensus"
	"github.com/NetSys/quilt/minion/docker"
	"github.com/NetSys/quilt/minion/elector"
	"github.com/NetSys/quilt/minion/network"
	"github.com/NetSys/quilt/minion/pprofile"
	"github.com/NetSys/quilt/minion/scheduler"
	"github.com/NetSys/quilt/minion/supervisor"
	"github.com/NetSys/quilt/util"

	log "github.com/Sirupsen/logrus"
)

func main() {
	// XXX Uncomment the following line to run the profiler
	//runProfiler(5 * time.Minute)

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

	for range conn.Trigger(db.MinionTable).C {
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

func runProfiler(duration time.Duration) {
	go func() {
		p := pprofile.New("minion")
		for {
			if err := p.TimedRun(duration); err != nil {
				log.Error(err)
			}
		}
	}()
}
