package main

import (
	"time"

	"github.com/NetSys/di/db"
	"github.com/coreos/etcd/Godeps/_workspace/src/golang.org/x/net/context"
	"github.com/coreos/etcd/client"
)

const electionTTL = 30
const bootDelay = 30
const leaderKey = "/minion/leader"

func watchLeader(conn db.Conn) {
	kapi, watch := etcdConnect()
	trigg := conn.TriggerTick(electionTTL, db.MinionTable)
	for {
		resp, _ := kapi.Get(ctx(), leaderKey, &client.GetOptions{Quorum: true})

		var leader string
		if resp != nil {
			leader = resp.Node.Value
		}

		conn.Transact(func(view db.Database) error {
			minions := view.SelectFromMinion(nil)
			if len(minions) == 1 {
				minions[0].LeaderIP = leader
				view.Commit(minions[0])
			}
			return nil
		})

		select {
		case <-watch:
		case <-trigg.C:
		}
	}
}

func campaign(conn db.Conn) {
	trigg := conn.TriggerTick(electionTTL/2, db.MinionTable)
	kapi, watchChan := etcdConnect()
	oldMaster := false

	for {
		select {
		case <-watchChan:
		case <-trigg.C:
		}

		minions := conn.SelectFromMinion(nil)
		master := len(minions) == 1 && minions[0].Role == db.Master
		switch {
		case oldMaster && !master:
			commitLeader(conn, false, "")
		case !oldMaster && master:
			// When we first boot, wait a bit for etcd to come up.
			log.Info("Starting leader election in %d seconds", bootDelay)
			time.Sleep(bootDelay * time.Second)

			// Update in case something changed while we were sleeping
			minions = conn.SelectFromMinion(nil)
			master = len(minions) == 1 && minions[0].Role == db.Master
		}
		oldMaster = master

		if !master {
			continue
		}

		IP := minions[0].PrivateIP
		if IP == "" {
			continue
		}

		opts := client.SetOptions{PrevExist: client.PrevNoExist,
			TTL: electionTTL * time.Second}
		if minions[0].Leader {
			opts.PrevExist = client.PrevExist
		}

		_, err := kapi.Set(ctx(), leaderKey, IP, &opts)
		if err == nil {
			commitLeader(conn, true, IP)
		} else {
			clientErr, ok := err.(client.Error)
			if !ok || clientErr.Code != client.ErrorCodeNodeExist {
				log.Warning("Error setting leader key: %s", err.Error())
				commitLeader(conn, false, "")

				// Give things a chance to settle down.
				time.Sleep(electionTTL * time.Second)
			} else {
				commitLeader(conn, false)
			}
		}
	}
}

func commitLeader(conn db.Conn, leader bool, ip ...string) {
	if len(ip) > 1 {
		panic("Not Reached")
	}

	conn.Transact(func(view db.Database) error {
		minions := view.SelectFromMinion(nil)
		if len(minions) == 1 {
			minions[0].Leader = leader

			if len(ip) == 1 {
				minions[0].LeaderIP = ip[0]
			}

			view.Commit(minions[0])
		}
		return nil
	})
}

func etcdConnect() (client.KeysAPI, <-chan struct{}) {
	var etcd client.Client
	for {
		var err error
		etcd, err = client.New(client.Config{
			Endpoints: []string{"http://127.0.0.1:2379"},
			Transport: client.DefaultTransport,
		})
		if err != nil {
			log.Warning("Failed to connect to ETCD: %s", err)
			time.Sleep(30 * time.Second)
			continue
		}

		break
	}

	kapi := client.NewKeysAPI(etcd)
	c := make(chan struct{})
	go func() {
		watcher := kapi.Watcher(leaderKey, nil)
		for {
			c <- struct{}{}
			watcher.Next(context.Background())
		}
	}()

	return kapi, c
}

func ctx() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), (electionTTL/4)*time.Second)
	return ctx
}
