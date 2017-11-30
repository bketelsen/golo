package main

import (
	"crypto/sha1"
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bketelsen/kodos"
	"github.com/bketelsen/kodos/gitconfig"
)

func check(err error) {
	if err != nil {
		fatal(err)
		os.Exit(1)
	}
}

func fatal(arg interface{}, args ...interface{}) {
	fmt.Fprint(os.Stderr, "fatal: ", arg)
	fmt.Fprintln(os.Stderr, args...)
	os.Exit(1)
}

func main() {
	pkgptr := flag.String("package", "", "the import path of your package")

	flag.Parse()
	dir, err := findreporoot(cwd())
	check(err)

	fmt.Println("Using", dir)

	workdir, err := ioutil.TempDir("", "kodos")
	check(err)

	pkgdir := filepath.Join(dir, ".kodos", "pkg")

	ctx := &kodos.Context{
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		Workdir: workdir,
		Pkgdir:  pkgdir,
		Bindir:  dir,
	}

	action := "build"
	var prefix string
	if *pkgptr == "" {
		prefix = guessPackage(dir)
		fmt.Println("Using guessed package", prefix)
	} else {
		prefix = *pkgptr
	}

	switch action {
	case "build":
		fmt.Println("load sources")
		srcs := loadSources(prefix, dir)
		for _, src := range srcs {
			fmt.Printf("loaded %s (%s)\n", src.ImportPath, src.Name)
		}
		fmt.Println("load dependencies")
		srcs = loadDependencies(dir, srcs...)
		pkgs := ctx.Transform(srcs...)
		fn, err := kodos.BuildPackages(pkgs...)
		check(err)
		check(fn())
	default:
		fatal("unknown action:", action)
	}
}

func guessPackage(dir string) string {
	url, err := gitconfig.OriginURL()
	check(err)
	fmt.Println(url, err)
	// hack - support git@ later
	url = strings.Replace(url, "https://", "", -1)

	return url

}

func cwd() string {
	wd, err := os.Getwd()
	check(err)
	return wd
}

// findreporoot returns the location of the closest .git directory
// relative to the dir provided.
func findreporoot(dir string) (string, error) {
	orig := dir
	for {
		path := filepath.Join(dir, ".git")
		fi, err := os.Stat(path)
		if err == nil && fi.IsDir() {
			return dir, nil
		}
		if err != nil && !os.IsNotExist(err) {
			check(err)
		}
		d := filepath.Dir(dir)
		if d == dir {
			// got to the root directory without
			return "", fmt.Errorf("could not locate .git in %s", orig)
		}
		dir = d
	}
}

func loadSources(prefix string, dir string) []*build.Package {
	f, err := os.Open(dir)
	check(err)
	files, err := f.Readdir(-1)
	check(err)
	f.Close()

	var srcs []*build.Package
	for _, fi := range files {
		name := fi.Name()
		if strings.HasPrefix(name, "_") || strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" {
			// ignore it
			continue
		}
		if fi.IsDir() {
			srcs = append(srcs, loadSources(path.Join(prefix, name), filepath.Join(dir, name))...)
		}
	}

	pkg, err := build.ImportDir(dir, 0)
	switch err := err.(type) {
	case nil:
		// ImportDir does not know the import path for this package
		// but we know the prefix, so fix it.
		pkg.ImportPath = prefix
		srcs = append(srcs, pkg)
	case (*build.NoGoError):
		// do nothing
	default:
		fmt.Println("DEFAULTED to throw the panic", err)
		check(err)
	}

	return srcs
}

func loadDependencies(rootdir string, srcs ...*build.Package) []*build.Package {
	load := func(path string) *build.Package {
		//		var found bool
		//		var err error
		//		fmt.Println("Rootdir:", rootdir)
		//		fmt.Println("checking for ", path)
		//		fmt.Println("Trying go source")
		dir := filepath.Join(runtime.GOROOT(), "src", path)
		if _, err := os.Stat(dir); err != nil {
			fmt.Println("Trying vendor")
			fmt.Println("rootdir, path", rootdir, path)
			dir = filepath.Join(rootdir, "vendor", path)
			fmt.Println("Trying ", dir)
			if _, err = os.Stat(dir); err != nil {
				fatal("cannot resolve path", path, err.Error())
			}
		}
		//			found = false
		//		} else {
		//			found = true
		//		}
		/*if !found && strings.Contains(path, "golang_org") {

			dir = filepath.Join(runtime.GOROOT(), "src", "vendor", path)
			if _, err = os.Stat(dir); err != nil {
				found = false
			} else {
				found = true
			}
		}

		if !found {
			fmt.Println("Trying vendor")
			fmt.Println("rootdir, path", rootdir, path)
			dir = filepath.Join(rootdir, "vendor", path)
			fmt.Println("Trying ", dir)
			if _, err = os.Stat(dir); err != nil {
				found = false
				fmt.Println("Not found")
			} else {
				fmt.Println("Found")
				found = true
			}
		}
		if !found {
			fatal("cannot resolve path ", path, err.Error())
		}
		*/
		return importPath(path, dir)
	}

	seen := make(map[string]bool)
	var walk func(string)
	walk = func(path string) {
		fmt.Println("Walk", path)
		if seen[path] {
			return
		}
		seen[path] = true
		pkg := load(path)
		srcs = append(srcs, pkg)
		for _, i := range pkg.Imports {
			walk(i)
		}
	}
	for _, src := range srcs {
		seen[src.ImportPath] = true
	}

	for _, src := range srcs[:] {
		for _, i := range src.Imports {
			fmt.Println(src, i)
			walk(i)
		}
	}
	return srcs
}

func register(rootdir, prefix, kind, arg string, next func(string) *build.Package) func(string) *build.Package {
	dir := cacheDir(rootdir, prefix+kind+"="+arg)
	fmt.Println("registered:", prefix, "@", arg)
	return func(path string) *build.Package {
		if !strings.HasPrefix(path, prefix) {
			return next(path)
		}
		fmt.Println("searching", path, "in", prefix, "@", arg)
		dir := filepath.Join(dir, path)
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			check(err)
		}
		return importPath(path, dir)
	}
}

func importPath(path, dir string) *build.Package {
	fmt.Println("checking import path for ", path, dir)
	pkg, err := build.ImportDir(dir, 0)
	check(err)
	// ImportDir does not know the import path for this package
	// but we know the prefix, so fix it.
	pkg.ImportPath = path
	return pkg
}

func cacheDir(rootdir, key string) string {
	hash := sha1.Sum([]byte(key))
	return filepath.Join(rootdir, ".kang", "cache", fmt.Sprintf("%x", hash[0:1]), fmt.Sprintf("%x", hash[1:]))
}
