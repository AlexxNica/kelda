package dsl

import (
	"io"

	log "github.com/Sirupsen/logrus"
)

// A Dsl is an abstract representation of the policy language.
type Dsl struct {
	spec ast
	ctx  evalCtx
}

// A Container may be instantiated in the dsl and queried by users.
type Container struct {
	Image   string
	Command []string

	Placement
	atomImpl
}

// A Placement constraint restricts where containers may be instantiated.
type Placement struct {
	Exclusive map[[2]string]struct{}
}

// A Connection allows containers implementing the From label to speak to containers
// implementing the To label in ports in the range [MinPort, MaxPort]
type Connection struct {
	From    string
	To      string
	MinPort int
	MaxPort int
}

// New parses and executes a dsl (in text form), and returns an abstract Dsl handle.
func New(reader io.Reader) (Dsl, error) {
	parsed, err := parse(reader)
	if err != nil {
		return Dsl{}, err
	}

	spec, ctx, err := eval(parsed)
	if err != nil {
		return Dsl{}, err
	}
	return Dsl{spec, ctx}, nil
}

// QueryContainers retreives all containers declared in dsl.
func (dsl Dsl) QueryContainers() []*Container {
	var containers []*Container
	for _, atom := range dsl.ctx.atoms {
		switch atom.(type) {
		case *Container:
			containers = append(containers, atom.(*Container))
		}
	}
	return containers
}

// QueryKeySlice returns the ssh keys associated with a label.
func (dsl Dsl) QueryKeySlice(label string) []string {
	result, ok := dsl.ctx.labels[label]
	if !ok {
		log.Warnf("%s undefined", label)
		return nil
	}

	var keys []string
	for _, val := range result {
		key, ok := val.(key)
		if !ok {
			log.Warning("%s: Requested []key, found %s", key, val)
			continue
		}

		parsedKeys, err := key.keys()
		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
				"key":   key,
			}).Warning("Failed to retrieve key.")
			continue
		}

		keys = append(keys, parsedKeys...)
	}

	return keys
}

// QueryConnections returns the connections declared in the dsl.
func (dsl Dsl) QueryConnections() []Connection {
	var connections []Connection
	for c := range dsl.ctx.connections {
		connections = append(connections, c)
	}
	return connections
}

// QueryInt returns an integer value defined in the dsl.
func (dsl Dsl) QueryInt(key string) int {
	result, ok := dsl.ctx.defines[astIdent(key)]
	if !ok {
		log.Warnf("%s undefined", key)
		return 0
	}

	val, ok := result.(astInt)
	if !ok {
		log.Warnf("%s: Requested int, found %s", key, val)
		return 0
	}

	return int(val)
}

// QueryString returns a string value defined in the dsl.
func (dsl Dsl) QueryString(key string) string {
	result, ok := dsl.ctx.defines[astIdent(key)]
	if !ok {
		log.Warnf("%s undefined", key)
		return ""
	}

	val, ok := result.(astString)
	if !ok {
		log.Warnf("%s: Requested string, found %s", key, val)
		return ""
	}

	return string(val)
}

// QueryStrSlice returns a string slice value defined in the dsl.
func (dsl Dsl) QueryStrSlice(key string) []string {
	result, ok := dsl.ctx.defines[astIdent(key)]
	if !ok {
		log.Warnf("%s undefined", key)
		return nil
	}

	val, ok := result.(astList)
	if !ok {
		log.Warnf("%s: Requested []string, found %s", key, val)
		return nil
	}

	slice := []string{}
	for _, val := range val {
		str, ok := val.(astString)
		if !ok {
			log.Warnf("%s: Requested []string, found %s", key, val)
			return nil
		}
		slice = append(slice, string(str))
	}

	return slice
}

// String returns the dsl in its code form.
func (dsl Dsl) String() string {
	return dsl.spec.String()
}
