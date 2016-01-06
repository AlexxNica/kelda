package dsl

import "fmt"

type funcImpl struct {
	do      func([]ast) (ast, error)
	minArgs int
}

var funcImplMap = map[astIdent]funcImpl{
	"+":        {arithFun(func(a, b int) int { return a + b }), 2},
	"-":        {arithFun(func(a, b int) int { return a - b }), 2},
	"*":        {arithFun(func(a, b int) int { return a * b }), 2},
	"/":        {arithFun(func(a, b int) int { return a / b }), 2},
	"%":        {arithFun(func(a, b int) int { return a % b }), 2},
	"list":     {listImpl, 0},
	"makeList": {makeListImpl, 2},
}

func arithFun(do func(a, b int) int) func([]ast) (ast, error) {
	return func(args []ast) (ast, error) {
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

func listImpl(args []ast) (ast, error) {
	return astList(args), nil
}

func makeListImpl(args []ast) (ast, error) {
	count, ok := args[0].(astInt)
	if !ok || count < 0 {
		return nil, fmt.Errorf("makeList must begin with a positive integer, "+
			"found: %s", args[0])
	}

	var result []ast
	for i := 0; i < int(count); i++ {
		result = append(result, args[1])
	}
	return astList(result), nil
}
