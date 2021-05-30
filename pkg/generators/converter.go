package generators

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/ast/astutil"
	"golang.org/x/tools/go/packages"
)

func ConverterGenerator(pkg *packages.Package, astf *ast.File) (*ast.File, bool) {
	var hasChanged bool
	resultNode := astutil.Apply(astf, func(cursor *astutil.Cursor) bool {
		funcDecl, ok := cursor.Node().(*ast.FuncDecl)
		if !ok {
			switch cursor.Node().(type) {
			case *ast.File:
				return true
			default:
				return false
			}
		}
		if funcDecl.Doc == nil {
			return false
		}

		comments := funcDecl.Doc.List
		var has bool
		for i := range comments {
			if strings.HasPrefix(comments[i].Text, "//ggg:convert") {
				comments = append(comments[0:i], comments[i+1:]...)
				funcDecl.Doc.List = comments
				has = true
				break
			}
		}
		if !has {
			return false
		}

		var inObj, outObj types.Object
		var inType, outType types.Type
		params := funcDecl.Type.Params
		results := funcDecl.Type.Results
		if params.NumFields() == 1 {
			inType = pkg.TypesInfo.TypeOf(params.List[0].Type)
			if len(params.List[0].Names) != 0 {
				inObj = pkg.TypesInfo.ObjectOf(params.List[0].Names[0])
			}
		}

		if results.NumFields() == 1 {
			outType = pkg.TypesInfo.TypeOf(results.List[0].Type)
			if len(results.List[0].Names) != 0 {
				outObj = pkg.TypesInfo.ObjectOf(results.List[0].Names[0])
			}
		}

		from, to := typeToString(inType), typeToString(outType)
		f := fromto{from: from, to: to}
		convert, ok := converters[f]
		if !ok {
			panic(fmt.Sprintf("unknown converter for %+v", f))
		}

		funcBody := convert(pkg.Fset, inObj, outObj, inType, outType)
		astutil.Apply(cursor.Node(), func(innerCursor *astutil.Cursor) bool {
			if innerCursor.Name() != "Body" {
				return true
			}
			innerCursor.Replace(funcBody)
			hasChanged = true
			return false
		}, nil)
		return false
	}, nil)
	return resultNode.(*ast.File), hasChanged
}

func typeToString(inType types.Type) string {
	inType = inType.Underlying()
	switch t := inType.(type) {
	case *types.Struct:
		return "Struct"
	case *types.Pointer:
		return "Ptr" + typeToString(t.Elem())
	default:
		return fmt.Sprintf("unknown %T", inType)
	}
}

type fromto struct {
	from string
	to   string
}
type convertResolver func(fset *token.FileSet, inObj, outObj types.Object, inType, outType types.Type) *ast.BlockStmt

var converters = map[fromto]convertResolver{
	{from: "Struct", to: "Struct"}:       structStructConverter,
	{from: "PtrStruct", to: "PtrStruct"}: structStructConverter,
	{from: "PtrStruct", to: "Struct"}:    structStructConverter,
	{from: "Struct", to: "PtrStruct"}:    structStructConverter,
}

func structStructConverter(fset *token.FileSet, inObj, outObj types.Object, inType, outType types.Type) *ast.BlockStmt {
	ctx := tplContext{
		In: tplType{
			T:   inType,
			Obj: inObj,
		},
		Out: tplType{
			T:   outType,
			Obj: outObj,
		},
	}

	var body []ast.Stmt
	if ctx.In.IsPtr() {
		var nilResultReturn ast.Stmt
		if ctx.Out.IsPtr() {
			nilResultReturn = &ast.ReturnStmt{
				Results: []ast.Expr{
					ast.NewIdent("nil"),
				},
			}
		} else {
			nilResultReturn = &ast.ReturnStmt{
				Results: []ast.Expr{
					exprFromString(fset, defaultValueExprTemplate(ctx)),
				},
			}
		}
		body = append(body, &ast.IfStmt{
			Init: nil,
			Cond: &ast.BinaryExpr{
				X:  ast.NewIdent(ctx.In.Obj.Name()),
				Op: token.EQL,
				Y:  ast.NewIdent("nil"),
			},
			Body: &ast.BlockStmt{
				List: []ast.Stmt{
					nilResultReturn,
				},
			},
			Else: nil,
		})
	}

	body = append(body, &ast.ReturnStmt{Results: []ast.Expr{
		exprFromString(fset, structResultTemplate(ctx)),
	}})

	return &ast.BlockStmt{
		List: body,
	}
}

func exprFromString(fset *token.FileSet, stringExpr string) ast.Expr {
	outExpr, err := parser.ParseExprFrom(fset, "", stringExpr, 0)
	if err != nil {
		panic(fmt.Sprintf("%v: %q", err, stringExpr))
	}
	return outExpr
}

type tplContext struct {
	In  tplType
	Out tplType
}

type tplType struct {
	T   types.Type
	Obj types.Object
}

func (ctx tplType) IsPtr() bool {
	_, ok := ctx.T.(*types.Pointer)
	return ok
}

func (ctx tplType) VarName() string {
	if ctx.Obj != nil {
		return ctx.Obj.Name()
	}
	return "in"
}

func (ctx tplType) Name() *types.Named {
	t := ctx.T
	if ctx.IsPtr() {
		t = t.(*types.Pointer).Elem()
	}
	return t.(*types.Named)
}

func (ctx tplType) Struct() *types.Struct {
	name := ctx.Name()
	return name.Underlying().(*types.Struct)
}

func (ctx tplType) FieldByName(name string) *types.Var {
	str := ctx.Struct()
	for i, n := 0, str.NumFields(); i < n; i++ {
		field := str.Field(i)
		if !field.Exported() {
			continue
		}
		if field.Name() == name {
			return field
		}
	}
	return nil
}

func (ctx tplType) Fields() []*types.Var {
	str := ctx.Struct()
	var fields []*types.Var
	for i, n := 0, str.NumFields(); i < n; i++ {
		field := str.Field(i)
		if !field.Exported() {
			continue
		}
		fields = append(fields, field)
	}
	return fields
}

func emptyField(field *types.Var) string {
	switch t := field.Type().(type) {
	case *types.Basic:
		switch t.Info() {
		case types.IsBoolean:
			return "false"
		case types.IsInteger:
			return "0"
		case types.IsUnsigned:
			return "0"
		case types.IsFloat:
			return "0"
		case types.IsComplex:
			return "0"
		case types.IsString:
			return `""`
		}
	case *types.Slice, *types.Map, *types.Chan, *types.Signature, *types.Pointer:
		return "nil"
	case *types.Named:
		//todo handle imported structs
		return t.Obj().Name() + "{}"
	}
	return "nil"
}

func convertField(varname string, in, out *types.Var) string {
	fieldExpr := fmt.Sprintf("%s.%s", varname, in.Name())
	if types.Identical(in.Type(), out.Type()) {
		return fieldExpr
	}
	inType, outType := in.Type(), out.Type()
	inPtr, inPtrOk := inType.(*types.Pointer)
	outPtr, outPtrOk := outType.(*types.Pointer)

	// X: *in.X
	if inPtrOk {
		if types.Identical(inPtr.Elem(), out.Type()) {
			return "*" + fieldExpr
		}
		inType = inPtr.Elem()
	}

	// X: &in.X
	if outPtrOk {
		if types.Identical(outPtr.Elem(), in.Type()) {
			return "&" + fieldExpr
		}
		outType = outPtr.Elem()
	}

	normIn, normOut := normalizeTypeName(inType), normalizeTypeName(outType)
	f := fromto{from: normIn, to: normOut}
	knownConv, ok := wellKnownConverters[f]
	if ok {
		return fmt.Sprintf(knownConv.Format, fieldExpr)
	}
	return UnknownConverterFormat(inType, outType, fieldExpr)
}

var UnknownConverterFormat = func(in, out types.Type, varname string) string {
	normIn, normOut := normalizeTypeName(in), normalizeTypeName(out)
	return fmt.Sprintf("new%sFrom%s(%s)", toUpperFirst(normIn), toUpperFirst(normOut), varname)
}

func toUpperFirst(s string) string {
	if len(s) == 0 {
		return ""
	}
	return strings.ToUpper(string(s[0])) + s[1:]
}

func normalizeTypeName(t types.Type) string {
	switch inType := t.(type) {
	case *types.Basic:
		return inType.Name()
	case *types.Slice:
		return "[]" + normalizeTypeName(inType.Elem())
	case *types.Named:
		return inType.Obj().Name()
	default:
		fmt.Printf("%T\n", t)
		return ""
	}
}

var wellKnownConverters = map[fromto]wellKnownConverter{
	{from: "int", to: "string"}:   {Format: "strconv.Itoa(%s)", Imports: []string{"strconv"}},
	{from: "int64", to: "string"}: {Format: "strconv.FormatInt(%s, 10)", Imports: []string{"strconv"}},
	{from: "int32", to: "string"}: {Format: "strconv.FormatInt(int64(%s), 10)", Imports: []string{"strconv"}},
	{from: "int", to: "int64"}:    {Format: "int64(%s)"},
	{from: "int64", to: "int"}:    {Format: "int(%s)"},
}

type wellKnownConverter struct {
	Format  string
	Imports []string
}
