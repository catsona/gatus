package config

import (
	"fmt"
	"reflect"
	"text/template"
	"text/template/parse"

	"git.pegasko.art/catsona/gstatus/internal/model"
)

func validateTemplate(tmpl *template.Template) error {
	root := reflect.TypeFor[model.TemplateData]()
	return walkTemplate(tmpl, tmpl.Tree.Root, root, map[string]reflect.Type{"$": root}, map[string]bool{})
}

func templateField(typ reflect.Type, fields []string) (reflect.Type, error) {
	for _, name := range fields {
		if typ == nil {
			return nil, nil
		}
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() == reflect.Map {
			typ = typ.Elem()
			continue
		}
		if method, ok := typ.MethodByName(name); ok && method.Type.NumOut() > 0 {
			typ = method.Type.Out(0)
			continue
		}
		if typ.Kind() != reflect.Struct {
			return nil, fmt.Errorf("field %s is unavailable on %s", name, typ)
		}
		field, ok := typ.FieldByName(name)
		if !ok || !field.IsExported() {
			return nil, fmt.Errorf("unknown field %s on %s", name, typ)
		}
		typ = field.Type
	}
	return typ, nil
}

func templateValue(node parse.Node, dot reflect.Type, variables map[string]reflect.Type) (reflect.Type, error) {
	switch node := node.(type) {
	case *parse.FieldNode:
		return templateField(dot, node.Ident)
	case *parse.VariableNode:
		return templateField(variables[node.Ident[0]], node.Ident[1:])
	case *parse.DotNode:
		return dot, nil
	case *parse.ChainNode:
		typ, err := templateValue(node.Node, dot, variables)
		if err != nil {
			return nil, err
		}
		return templateField(typ, node.Field)
	case *parse.PipeNode:
		var result reflect.Type
		for _, command := range node.Cmds {
			for index, arg := range command.Args {
				typ, err := templateValue(arg, dot, variables)
				if err != nil {
					return nil, err
				}
				if index == 0 {
					result = typ
				}
			}
			if function, ok := command.Args[0].(*parse.IdentifierNode); ok && function.Ident == "index" && len(command.Args) >= 3 {
				var err error
				result, err = templateValue(command.Args[1], dot, variables)
				if err != nil {
					return nil, err
				}
				for range command.Args[2:] {
					if result == nil {
						break
					}
					for result.Kind() == reflect.Pointer {
						result = result.Elem()
					}
					switch result.Kind() {
					case reflect.Array, reflect.Slice, reflect.Map:
						result = result.Elem()
					case reflect.String:
						result = reflect.TypeFor[byte]()
					default:
						return nil, fmt.Errorf("cannot index %s", result)
					}
				}
			}
		}
		for _, declaration := range node.Decl {
			variables[declaration.Ident[0]] = result
		}
		return result, nil
	}
	return nil, nil
}

func walkTemplate(tmpl *template.Template, list *parse.ListNode, dot reflect.Type, variables map[string]reflect.Type, visiting map[string]bool) error {
	if list == nil {
		return nil
	}
	local := make(map[string]reflect.Type, len(variables))
	for name, typ := range variables {
		local[name] = typ
	}

	for _, node := range list.Nodes {
		var branch *parse.BranchNode
		var kind string
		switch node := node.(type) {
		case *parse.ActionNode:
			if _, err := templateValue(node.Pipe, dot, local); err != nil {
				return err
			}
		case *parse.IfNode:
			branch, kind = &node.BranchNode, "if"
		case *parse.WithNode:
			branch, kind = &node.BranchNode, "with"
		case *parse.RangeNode:
			branch, kind = &node.BranchNode, "range"
		case *parse.TemplateNode:
			typ, err := templateValue(node.Pipe, dot, local)
			if err != nil {
				return err
			}
			other := tmpl.Lookup(node.Name)
			if other != nil && !visiting[node.Name] {
				visiting[node.Name] = true
				err = walkTemplate(tmpl, other.Tree.Root, typ, map[string]reflect.Type{"$": typ}, visiting)
				delete(visiting, node.Name)
				if err != nil {
					return err
				}
			}
		}
		if branch == nil {
			continue
		}

		typ, err := templateValue(branch.Pipe, dot, local)
		if err != nil {
			return err
		}
		nested := dot
		if kind == "with" {
			nested = typ
		} else if kind == "range" {
			if typ != nil && (typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array || typ.Kind() == reflect.Map || typ.Kind() == reflect.Chan) {
				nested = typ.Elem()
			} else {
				nested = nil
			}
			for _, declaration := range branch.Pipe.Decl {
				local[declaration.Ident[0]] = nested
			}
		}
		if err := walkTemplate(tmpl, branch.List, nested, local, visiting); err != nil {
			return err
		}
		if err := walkTemplate(tmpl, branch.ElseList, dot, local, visiting); err != nil {
			return err
		}
	}
	return nil
}
