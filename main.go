package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"go/token"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"

	"ggg/pkg/generators"

	"golang.org/x/tools/go/packages"
)

//-go:generate go get -u github.com/valyala/quicktemplate/qtc
//go:generate qtc -dir=pkg/generators

var (
	flagW     = flag.String("w", "", "source file")
	flagDebug = flag.Bool("debug", false, "enables debug mode, so output would be written to .out files")
)

func main() {
	flag.Parse()
	if *flagW == "" {
		log.Println("empty source file flag")
		return
	}
	filename := *flagW
	fileContent, err := ioutil.ReadFile(filename)
	if err != nil {
		log.Fatalf("read source file: %v", err)
	}

	mode := packages.NeedDeps | packages.NeedTypes | packages.NeedImports | packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedCompiledGoFiles
	fset := token.NewFileSet()
	cfg := &packages.Config{Mode: mode, Fset: fset}
	pkgs, err := packages.Load(cfg, filepath.Dir(filename))
	if err != nil {
		panic(err)
	}

	pkg := pkgs[0]
	idx := indexOfStrings(pkg.CompiledGoFiles, filename)
	if idx < 0 {
		fmt.Println("not found file in package")
		return
	}
	astf := pkg.Syntax[idx]
	fset = pkg.Fset

	astf, hasChanged := generators.ConverterGenerator(pkg, astf)

	if !hasChanged {
		return
	}

	var b bytes.Buffer
	err = format.Node(&b, fset, astf)
	if err != nil {
		log.Fatalf("cannot format file: %v", err)
	}

	if bytes.Equal(fileContent, b.Bytes()) {
		return
	}

	if *flagDebug {
		filename += ".out"
	}
	// On Windows, we need to re-set the permissions from the file. See golang/go#38225.
	var perms os.FileMode
	if fi, err := os.Stat(filename); err == nil {
		perms = fi.Mode() & os.ModePerm
	} else if os.IsNotExist(err) {
		perms = os.ModePerm
	}
	err = ioutil.WriteFile(filename, b.Bytes(), perms)
	if err != nil {
		log.Fatalf("cant write file: %v", err)
	}
}

func indexOfStrings(files []string, filename string) int {
	for i := range files {
		if files[i] == filename {
			return i
		}
	}
	return -1
}
