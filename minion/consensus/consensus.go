package consensus

import (
	"strings"
	"time"

	"github.com/coreos/etcd/Godeps/_workspace/src/golang.org/x/net/context"

	"github.com/coreos/etcd/client"
	"github.com/op/go-logging"
)

var log = logging.MustGetLogger("etcd")
var ErrClusterUnavailable = client.ErrClusterUnavailable

type Store interface {
	Watch(path string, rateLimit time.Duration) chan struct{}
	Mkdir(dir string) error
	GetTree(dir string) (Tree, error)
	Get(path string) (string, error)
	Delete(path string) error
	Create(path, value string, ttl time.Duration) error
	Update(path, value string, ttl time.Duration) error
	Set(path, value string) error
}

type store struct {
	kapi client.KeysAPI
}

type Tree struct {
	Key      string
	Value    string
	Children map[string]Tree
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

	return store{client.NewKeysAPI(etcd)}
}

func (s store) Watch(path string, rateLimit time.Duration) chan struct{} {
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

func (s store) Mkdir(dir string) error {
	_, err := s.kapi.Set(ctx(), dir, "", &client.SetOptions{
		Dir:       true,
		PrevExist: client.PrevNoExist,
	})
	return err
}

func (s store) GetTree(dir string) (Tree, error) {
	resp, err := s.kapi.Get(ctx(), dir, &client.GetOptions{
		Recursive: true,
		Sort:      false,
		Quorum:    true,
	})
	if err != nil {
		return Tree{}, err
	}

	var rec func(*client.Node) Tree
	rec = func(node *client.Node) Tree {
		keySlice := strings.Split(node.Key, "/")
		tree := Tree{
			Key:      keySlice[len(keySlice)-1],
			Value:    node.Value,
			Children: map[string]Tree{},
		}

		for _, child := range node.Nodes {
			ct := rec(child)
			tree.Children[ct.Key] = ct
		}

		return tree
	}

	return rec(resp.Node), nil
}

func (s store) Get(path string) (string, error) {
	resp, err := s.kapi.Get(ctx(), path, &client.GetOptions{
		Quorum: true,
	})
	if err != nil {
		return "", err
	}

	return resp.Node.Value, nil
}

func (s store) Delete(path string) error {
	_, err := s.kapi.Delete(ctx(), path, nil)
	return err
}

func (s store) Create(path, value string, ttl time.Duration) error {
	_, err := s.kapi.Set(ctx(), path, value,
		&client.SetOptions{PrevExist: client.PrevNoExist, TTL: ttl})
	return err
}

func (s store) Update(path, value string, ttl time.Duration) error {
	_, err := s.kapi.Set(ctx(), path, value,
		&client.SetOptions{PrevExist: client.PrevExist, TTL: ttl})
	return err
}

func (s store) Set(path, value string) error {
	_, err := s.kapi.Set(ctx(), path, value, nil)
	return err
}

func ctx() context.Context {
	ctx, _ := context.WithTimeout(context.Background(), 5*time.Second)
	return ctx
}
