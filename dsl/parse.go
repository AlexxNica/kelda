package dsl

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/scanner"
)

var ErrUnbalancedParens = errors.New("Unbalanced Parenthesis")
var ErrBinding = errors.New("Error parsing bindings")

func parse(reader io.Reader) (astRoot, error) {
	var s scanner.Scanner
	s.Init(reader)

	scanErrors := []string{}
	s.Error = func(s *scanner.Scanner, msg string) {
		scanErrors = append(scanErrors, msg)
	}

	p1, err := parseText(&s, 0)
	if s.ErrorCount != 0 {
		return nil, errors.New(strings.Join(scanErrors, "\n"))
	} else if err != nil {
		return nil, err
	}

	ast, err := parseList(p1, true)
	if err != nil {
		return nil, err
	}

	return astRoot(ast), nil
}

func parseText(s *scanner.Scanner, depth int) ([]interface{}, error) {
	slice := []interface{}{}
	for {
		switch s.Scan() {
		case scanner.Ident:
			slice = append(slice, astIdent(s.TokenText()))

		case scanner.Int:
			x, _ := strconv.Atoi(s.TokenText())
			slice = append(slice, astInt(x))
		case scanner.String:
			str := strings.Trim(s.TokenText(), "\"")
			slice = append(slice, astString(str))
		case '(':
			sexp, err := parseText(s, depth+1)
			if err != nil {
				return nil, err
			}

			slice = append(slice, sexp)

		case ')':
			if depth == 0 {
				return nil, ErrUnbalancedParens
			}
			return slice, nil
		case scanner.EOF:
			if depth != 0 {
				return nil, ErrUnbalancedParens
			}
			return slice, nil

		default:
			slice = append(slice, s.TokenText())
		}
	}
}

func parseInterface(p1 interface{}, root bool) (ast, error) {
	var list []interface{}
	switch elem := p1.(type) {
	case []interface{}:
		list = elem
	case astInt:
		return elem, nil
	case astIdent:
		return elem, nil
	case astString:
		return elem, nil
	default:
		return nil, errors.New(fmt.Sprintf("Bad element: %s", elem))
	}

	if len(list) == 0 {
		return nil, errors.New(fmt.Sprintf("Bad element: %s", list))
	}

	switch first := list[0].(type) {
	case string:
		switch first {
		case "+":
			do := func(a, b int) int { return a + b }
			return parseArith("+", do, list[1:])
		case "-":
			do := func(a, b int) int { return a - b }
			return parseArith("-", do, list[1:])
		case "*":
			do := func(a, b int) int { return a * b }
			return parseArith("*", do, list[1:])
		case "/":
			do := func(a, b int) int { return a / b }
			return parseArith("/", do, list[1:])
		case "%":
			do := func(a, b int) int { return a % b }
			return parseArith("%", do, list[1:])
		}
	case astIdent:
		switch first {
		case "let":
			if len(list) != 3 {
				return nil, errors.New(fmt.Sprintf(
					"Not enough arguments: %s", list))
			}

			binds, err := parseBindList(list[1])
			if err != nil {
				return nil, err
			}

			tree, err := parseInterface(list[2], false)
			if err != nil {
				return nil, err
			}

			return astLet{binds, tree}, nil
		case "list":
			return parseList(list[1:], false)
		case "define":
			if root != true {
				return nil,
					errors.New("'define' must be at the top level")
			}

			bind, err := parseBind(list[1:])
			if err != nil {
				return nil, err
			}
			return astDefine(bind), nil
		}
	}

	return nil, errors.New("Lists must start with operators")
}

func parseList(list []interface{}, root bool) (astListOp, error) {
	result := []ast{}
	for _, elem := range list {
		parsed, err := parseInterface(elem, root)
		if err != nil {
			return nil, err
		}
		result = append(result, parsed)
	}
	return astListOp(result), nil
}

func parseBindList(bindIface interface{}) ([]astBind, error) {
	list, ok := bindIface.([]interface{})
	if !ok {
		return nil, ErrBinding
	}

	result := []astBind{}
	for _, elem := range list {
		bind, err := parseBind(elem)
		if err != nil {
			return nil, err
		}
		result = append(result, bind)
	}

	return result, nil
}

func parseBind(iface interface{}) (astBind, error) {
	pair, ok := iface.([]interface{})
	if !ok {
		return astBind{}, ErrBinding
	}

	if len(pair) != 2 {
		return astBind{}, ErrBinding
	}

	name, ok := pair[0].(astIdent)
	if !ok {
		return astBind{}, ErrBinding
	}

	tree, err := parseInterface(pair[1], false)
	if err != nil {
		return astBind{}, err
	}

	return astBind{name, tree}, nil
}

func parseArith(name string, do func(int, int) int, args []interface{}) (ast, error) {
	if len(args) < 2 {
		return nil, errors.New("Not Enough Arguments")
	}

	astArgs := []ast{}
	for _, iface := range args {
		tree, err := parseInterface(iface, false)
		if err != nil {
			return nil, err
		}
		astArgs = append(astArgs, tree)
	}

	return astArith{name, do, astArgs}, nil
}
