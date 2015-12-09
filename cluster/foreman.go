package cluster

import (
	"sync"
	"time"

	"golang.org/x/net/context"

	. "github.com/NetSys/di/minion/proto"
)

func foremanQueryMinions(instances []Instance) {
	forEachInstance(instances, queryMinion)
}

func foremanWriteMinions(instances []Instance) {
	forEachInstance(instances, writeMinion)
}

func queryMinion(inst *Instance) {
	inst.role = PENDING

	client := (*inst).minionClient()
	if client == nil {
		return
	}

	ctx := defaultCTX()
	cfg, err := client.GetMinionConfig(ctx, &Request{})
	if err != nil {
		if ctx.Err() == nil {
			log.Info("Failed to get MinionConfig: %s", err)
		}
		return
	}

	inst.role = roleFromMinion(cfg.Role)
	inst.EtcdToken = cfg.EtcdToken
}

func writeMinion(inst *Instance) {
	client := inst.minionClient()
	if client == nil || inst.PrivateIP == nil {
		return
	}

	reply, err := client.SetMinionConfig(defaultCTX(), &MinionConfig{
		ID:        inst.Id,
		Role:      roleToMinion(inst.role),
		PrivateIP: *inst.PrivateIP,
		EtcdToken: inst.EtcdToken,
	})
	if err != nil {
		log.Warning("Failed to set minion config: %s", err)
	} else if reply.Success == false {
		log.Warning("Unsuccessful minion reply: %s", reply.Error)
	}
}

func forEachInstance(instances []Instance, do func(inst *Instance)) {
	var wg sync.WaitGroup

	wg.Add(len(instances))
	defer wg.Wait()

	for i := range instances {
		inst := &instances[i]
		go func() {
			defer wg.Done()
			do(inst)

		}()
	}
}

func defaultCTX() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)
	return ctx
}
