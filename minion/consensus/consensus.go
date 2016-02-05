package consensus

import (
	"time"

	"github.com/coreos/etcd/Godeps/_workspace/src/golang.org/x/net/context"

	"github.com/coreos/etcd/client"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("etcd")

type Store struct {
	kapi client.KeysAPI
}

func NewStore() Store {
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

	return Store{client.NewKeysAPI(etcd)}
}

func (s Store) Watch(path string, rateLimit time.Duration) chan struct{} {
	c := make(chan struct{})
	go func() {
		watcher := s.kapi.Watcher(path, &client.WatcherOptions{Recursive: true})
		for {
			c <- struct{}{}
			time.Sleep(rateLimit)
			watcher.Next(context.Background())
		}
	}()

	return c
}

func (s Store) Mkdir(dir string) error {
	_, err := s.kapi.Set(ctx(), dir, "", &client.SetOptions{
		Dir:       true,
		PrevExist: client.PrevNoExist,
	})
	return err
}

func (s Store) GetDir(dir string) (map[string]string, error) {
	resp, err := s.kapi.Get(ctx(), dir, &client.GetOptions{
		Recursive: true,
		Sort:      false,
		Quorum:    true,
	})
	if err != nil {
		return nil, err
	}

	result := make(map[string]string)
	for _, node := range resp.Node.Nodes {
		result[node.Key] = node.Value
	}
	return result, nil
}

func (s Store) Get(path string) (string, error) {
	resp, err := s.kapi.Get(ctx(), path, &client.GetOptions{
		Quorum: true,
	})
	if err != nil {
		return "", err
	}

	return resp.Node.Value, nil
}

func (s Store) Delete(path string) error {
	_, err := s.kapi.Delete(ctx(), path, nil)
	return err
}

func (s Store) Create(path, value string, ttl time.Duration) error {
	_, err := s.kapi.Set(ctx(), path, value,
		&client.SetOptions{PrevExist: client.PrevNoExist, TTL: ttl})
	return err
}

func (s Store) Update(path, value string, ttl time.Duration) error {
	_, err := s.kapi.Set(ctx(), path, value,
		&client.SetOptions{PrevExist: client.PrevExist, TTL: ttl})
	return err
}

func (s Store) Set(path, value string) error {
	_, err := s.kapi.Set(ctx(), path, value, nil)
	return err
}

func ctx() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	return ctx
}
