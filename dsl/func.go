package dsl

import (
	"fmt"
	"strings"

	log "github.com/Sirupsen/logrus"
)

type funcImpl struct {
	do      func(*evalCtx, []ast) (ast, error)
	minArgs int
}

var funcImplMap = map[astIdent]funcImpl{
	"%":                {arithFun(func(a, b int) int { return a % b }), 2},
	"*":                {arithFun(func(a, b int) int { return a * b }), 2},
	"+":                {arithFun(func(a, b int) int { return a + b }), 2},
	"-":                {arithFun(func(a, b int) int { return a - b }), 2},
	"/":                {arithFun(func(a, b int) int { return a / b }), 2},
	"connect":          {connectImpl, 3},
	"docker":           {dockerImpl, 1},
	"label":            {labelImpl, 2},
	"list":             {listImpl, 0},
	"makeList":         {makeListImpl, 2},
	"sprintf":          {sprintfImpl, 1},
	"placement":        {placementImpl, 3},
	"githubKey":        {githubKeyImpl, 1},
	"plaintextKey":     {plaintextKeyImpl, 1},
	"size":             {sizeImpl, 1},
	"provider":         {providerImpl, 1},
	"machine":          {machineImpl, 0},
	"machineAttribute": {machineAttributeImpl, 2},
	"ram":              {rangeImpl("ram"), 1},
	"cpu":              {rangeImpl("cpu"), 1},
}

// XXX: support float operators?
func arithFun(do func(a, b int) int) func(*evalCtx, []ast) (ast, error) {
	return func(ctx *evalCtx, argsAst []ast) (ast, error) {
		args, err := evalArgs(ctx, argsAst)
		if err != nil {
			return nil, err
		}

		var ints []int
		for _, arg := range args {
			ival, ok := arg.(astInt)
			if !ok {
				err := fmt.Errorf("bad arithmetic argument: %s", arg)
				return nil, err
			}
			ints = append(ints, int(ival))
		}

		total := ints[0]
		for _, x := range ints[1:] {
			total = do(total, x)
		}

		return astInt(total), nil
	}
}

func dockerImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	evalArgs, err := evalArgs(ctx, argsAst)
	if err != nil {
		return nil, err
	}

	var args []string
	for _, ev := range evalArgs {
		arg, ok := ev.(astString)
		if !ok {
			return nil, fmt.Errorf("docker arguments must be strings: %s",
				ev)
		}
		args = append(args, string(arg))
	}

	index := len(ctx.atoms)

	var command []string
	if len(args) > 1 {
		command = args[1:]
	}

	container := &Container{
		Image: args[0], Command: command,
		Placement: Placement{make(map[[2]string]struct{})},
	}
	ctx.atoms = append(ctx.atoms, container)

	return astAtom{astFunc{astIdent("docker"), dockerImpl, evalArgs}, index}, nil
}

func githubKeyImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	evalArgs, err := evalArgs(ctx, argsAst)
	if err != nil {
		return nil, err
	}
	index := len(ctx.atoms)
	key := &githubKey{username: string(evalArgs[0].(astString))}
	ctx.atoms = append(ctx.atoms, key)

	return astAtom{astFunc{astIdent("githubKey"), githubKeyImpl, evalArgs}, index}, nil
}

func plaintextKeyImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	evalArgs, err := evalArgs(ctx, argsAst)
	if err != nil {
		return nil, err
	}

	index := len(ctx.atoms)
	key := &plaintextKey{key: string(evalArgs[0].(astString))}
	ctx.atoms = append(ctx.atoms, key)

	return astAtom{astFunc{astIdent("plaintextKey"), plaintextKeyImpl, evalArgs}, index}, nil
}

func placementImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	args, err := evalArgs(ctx, argsAst)
	if err != nil {
		return nil, err
	}

	str, ok := args[0].(astString)
	if !ok {
		return nil, fmt.Errorf("placement type must be a string, found: %s", args[0])
	}
	ptype := string(str)

	var labels []string
	for _, arg := range args[1:] {
		str, ok = arg.(astString)
		if !ok {
			return nil, fmt.Errorf("placement arg must be a string, found: %s", arg)
		}
		labels = append(labels, string(str))
	}

	parsedLabels := make(map[[2]string]struct{})
	for i := 0; i < len(labels)-1; i++ {
		for j := i + 1; j < len(labels); j++ {
			if labels[i] < labels[j] {
				parsedLabels[[2]string{labels[i], labels[j]}] = struct{}{}
			} else {
				parsedLabels[[2]string{labels[j], labels[i]}] = struct{}{}
			}
		}
	}

	switch ptype {
	case "exclusive":
		for _, label := range labels {
			for _, c := range ctx.labels[label] {
				c, ok := c.(*Container)
				if !ok {
					return nil, fmt.Errorf("placement labels must contain containers: %s", label)
				}
				for k, v := range parsedLabels {
					c.Placement.Exclusive[k] = v
				}
			}
		}
	default:
		return nil, fmt.Errorf("not a valid placement type: %s", ptype)
	}

	return astFunc{astIdent("placement"), placementImpl, args}, nil
}

func setMachineAttributes(machine *Machine, args []ast) error {
	for _, arg := range flatten(args) {
		switch arg.(type) {
		case astProvider:
			machine.Provider = string(arg.(astProvider))
		case astSize:
			machine.Size = string(arg.(astSize))
		case astRange:
			r := arg.(astRange)
			dslr := Range{Min: float64(r.min), Max: float64(r.max)}
			switch string(r.ident) {
			case "ram":
				machine.RAM = dslr
			case "cpu":
				machine.CPU = dslr
			default:
				return fmt.Errorf("unrecognized argument to machine definition: %s", arg)
			}
		default:
			return fmt.Errorf("unrecognized argument to machine definition: %s", arg)
		}
	}
	return nil
}

func machineImpl(ctx *evalCtx, args []ast) (ast, error) {
	evalArgs, err := evalArgs(ctx, args)
	if err != nil {
		return nil, err
	}

	index := len(ctx.atoms)
	machine := &Machine{}
	err = setMachineAttributes(machine, evalArgs)
	if err != nil {
		return nil, err
	}
	ctx.atoms = append(ctx.atoms, machine)

	return astAtom{astFunc{astIdent("machine"), machineImpl, evalArgs}, index}, nil
}

func machineAttributeImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	evalArgs, err := evalArgs(ctx, argsAst)
	if err != nil {
		return nil, err
	}

	key, ok := evalArgs[0].(astString)
	if !ok {
		return nil, fmt.Errorf("machineAttribute key must be a string: %s", evalArgs[0])
	}

	target, ok := ctx.labels[string(key)]
	if !ok {
		return nil, fmt.Errorf("machineAttribute key not defined: %s", key)
	}

	for _, val := range target {
		machine, ok := val.(*Machine)
		if !ok {
			return nil, fmt.Errorf("bad type, cannot change machine attributes: %s", val)
		}
		err = setMachineAttributes(machine, evalArgs[1:])
		if err != nil {
			return nil, err
		}
	}

	return astFunc{astIdent("machineAttribute"), machineAttributeImpl, evalArgs}, nil
}

func providerImpl(ctx *evalCtx, args []ast) (ast, error) {
	evalArgs, err := evalArgs(ctx, args)
	if err != nil {
		return nil, err
	}

	return astProvider((evalArgs[0].(astString))), nil
}

func sizeImpl(ctx *evalCtx, args []ast) (ast, error) {
	evalArgs, err := evalArgs(ctx, args)
	if err != nil {
		return nil, err
	}

	return astSize((evalArgs[0].(astString))), nil
}

func toFloat(x ast) (astFloat, error) {
	switch x.(type) {
	case astInt:
		return astFloat(x.(astInt)), nil
	case astFloat:
		return x.(astFloat), nil
	default:
		return astFloat(0), fmt.Errorf("%v is not convertable to a float", x)
	}

}

func rangeImpl(rangeType string) func(*evalCtx, []ast) (ast, error) {
	return func(ctx *evalCtx, args__ []ast) (ast, error) {
		evalArgs, err := evalArgs(ctx, args__)
		if err != nil {
			return nil, err
		}

		var max astFloat
		var maxErr error
		if len(evalArgs) > 1 {
			max, maxErr = toFloat(evalArgs[1])
		}
		min, minErr := toFloat(evalArgs[0])

		if minErr != nil || maxErr != nil {
			return nil, fmt.Errorf("range arguments must be convertable to floats: %v", evalArgs)
		}

		return astRange{ident: astIdent(rangeType), min: min, max: max}, nil
	}
}

func connectImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	args, err := evalArgs(ctx, argsAst)
	if err != nil {
		return nil, err
	}

	var min, max int
	switch t := args[0].(type) {
	case astInt:
		min, max = int(t), int(t)
	case astList:
		if len(t) != 2 {
			return nil, fmt.Errorf("port range must have two ints: %s", t)
		}

		minAst, minOK := t[0].(astInt)
		maxAst, maxOK := t[1].(astInt)
		if !minOK || !maxOK {
			return nil, fmt.Errorf("port range must have two ints: %s", t)
		}

		min, max = int(minAst), int(maxAst)
	default:
		return nil, fmt.Errorf("port range must be an int or a list of ints:"+
			" %s", args[0])
	}

	if min < 0 || max > 65535 {
		return nil, fmt.Errorf("invalid port range: [%d, %d]", min, max)
	}

	if min > max {
		return nil, fmt.Errorf("invalid port range: [%d, %d]", min, max)
	}

	var labels []string
	for _, arg := range flatten(args[1:]) {
		label, ok := arg.(astString)
		if !ok {
			err := fmt.Errorf("connect applies to labels: %s", arg)
			return nil, err
		}

		if _, ok := ctx.labels[string(label)]; !ok {
			return nil, fmt.Errorf("connect undefined label: %s",
				label)
		}

		labels = append(labels, string(label))
	}

	from := labels[0]
	for _, to := range labels[1:] {
		cn := Connection{
			From:    from,
			To:      to,
			MinPort: min,
			MaxPort: max,
		}
		ctx.connections[cn] = struct{}{}
	}

	newArgs := args[0:1]
	for _, label := range labels {
		newArgs = append(newArgs, astString(label))
	}

	return astFunc{astIdent("connect"), connectImpl, newArgs}, nil
}

func labelImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	args, err := evalArgs(ctx, argsAst)
	if err != nil {
		return nil, err
	}

	str, ok := args[0].(astString)
	if !ok {
		return nil, fmt.Errorf("label must be a string, found: %s", args[0])
	}
	label := string(str)
	if label != strings.ToLower(label) {
		log.Error("Labels must be lowercase, sorry! https://github.com/docker/swarm/issues/1795")
	}

	if _, ok := ctx.labels[label]; ok {
		return nil, fmt.Errorf("attempt to redefine label: %s", label)
	}

	var atoms []atom
	for _, elem := range flatten(args[1:]) {
		switch t := elem.(type) {
		case astAtom:
			atoms = append(atoms, ctx.atoms[t.index])
		case astString:
			children, ok := ctx.labels[string(t)]
			if !ok {
				return nil, fmt.Errorf("undefined label: %s", t)
			}

			for _, c := range children {
				atoms = append(atoms, c)
			}
		default:
			return nil, fmt.Errorf("label must apply to atoms or other"+
				" labels, found: %s", elem)
		}
	}

	for _, a := range atoms {
		labels := a.Labels()
		if len(labels) > 0 && labels[len(labels)-1] == label {
			// It's possible that the same container appears in the list
			// twice.  If that's the case, we'll end up labelling it multiple
			// times unless we check it's most recently added label.
			continue
		}

		a.SetLabels(append(labels, label))
	}

	ctx.labels[label] = atoms

	return astFunc{astIdent("label"), labelImpl, args}, nil
}

func listImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	args, err := evalArgs(ctx, argsAst)
	return astList(args), err
}

func makeListImpl(ctx *evalCtx, args []ast) (ast, error) {
	eval, err := args[0].eval(ctx)
	if err != nil {
		return nil, err
	}

	count, ok := eval.(astInt)
	if !ok || count < 0 {
		return nil, fmt.Errorf("makeList must begin with a positive integer, "+
			"found: %s", args[0])
	}

	var result []ast
	for i := 0; i < int(count); i++ {
		eval, err := args[1].eval(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, eval)
	}
	return astList(result), nil
}

func sprintfImpl(ctx *evalCtx, argsAst []ast) (ast, error) {
	args, err := evalArgs(ctx, argsAst)
	if err != nil {
		return nil, err
	}

	format, ok := args[0].(astString)
	if !ok {
		return nil, fmt.Errorf("sprintf format must be a string: %s", args[0])
	}

	var ifaceArgs []interface{}
	for _, arg := range args[1:] {
		var iface interface{}
		switch t := arg.(type) {
		case astString:
			iface = string(t)
		case astInt:
			iface = int(t)
		default:
			iface = t
		}

		ifaceArgs = append(ifaceArgs, iface)
	}

	return astString(fmt.Sprintf(string(format), ifaceArgs...)), nil
}

func evalArgs(ctx *evalCtx, args []ast) ([]ast, error) {
	var result []ast
	for _, a := range args {
		eval, err := a.eval(ctx)
		if err != nil {
			return nil, err
		}
		result = append(result, eval)
	}

	return result, nil
}

func flatten(lst []ast) []ast {
	var result []ast

	for _, l := range lst {
		switch t := l.(type) {
		case astList:
			result = append(result, flatten(t)...)
		default:
			result = append(result, l)
		}
	}

	return result
}
