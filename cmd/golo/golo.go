package main

import (
	"crypto/sha1"
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"bitbucket.org/rw_grim/govcs"

	"github.com/bketelsen/golo"
)

var verbose bool

func report(vals ...interface{}) {
	if verbose {
		fmt.Println(vals...)
	}
}

func reportf(format string, vals ...interface{}) {
	if verbose {
		fmt.Printf(format, vals...)
	}
}

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
	flag.BoolVar(&verbose, "verbose", false, "verbose output")
	flag.Parse()

	// icky
	golo.Verbose = verbose

	vcs, err := govcs.Detect(cwd())
	check(err)

	dir := vcs.Root()

	report("Repository Root", dir)

	workdir, err := ioutil.TempDir("", "golo")
	check(err)

	pkgdir := filepath.Join(dir, ".golo", "pkg")

	ctx := &golo.Context{
		GOOS:    runtime.GOOS,
		GOARCH:  runtime.GOARCH,
		Workdir: workdir,
		Pkgdir:  pkgdir,
		Bindir:  dir,
	}

	action := "build"
	var prefix string
	if *pkgptr == "" {
		prefix, err = guessPackage(vcs.Remote(""))
		check(err)
		report("Using guessed package", prefix)
	} else {
		prefix = *pkgptr
		report("Using provided package", prefix)
	}

	switch action {
	case "build":
		report("load local sources")
		srcs := loadSources(prefix, dir)
		for _, src := range srcs {
			reportf("loaded %s (%s)\n", src.ImportPath, src.Name)
		}
		report("load dependencies")
		srcs = loadDependencies(dir, srcs...)
		pkgs := ctx.Transform(srcs...)
		for _, p := range pkgs {
			reportf("package :  %s\n", p.ImportPath)
		}
		fn, err := golo.BuildPackages(pkgs...)
		check(err)
		check(fn())
	default:
		fatal("unknown action:", action)
	}
}

func guessPackage(remote string) (string, error) {
	uri, err := url.Parse(remote)
	if err != nil {
		return "", err
	}

	return uri.Host + uri.Path, nil
}

func cwd() string {
	wd, err := os.Getwd()
	check(err)
	return wd
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
		dir := filepath.Join(runtime.GOROOT(), "src", path)
		if _, err := os.Stat(dir); err != nil {
			reportf("Trying vendor directory %s for dependency %sdir. Rootdir: ", dir, path, rootdir)
			dir = filepath.Join(rootdir, "vendor", path)
			report("\tChecking", dir)
			if _, err = os.Stat(dir); err != nil {
				fatal("cannot resolve path", path, err.Error())
			}
		}
		return importPath(path, dir)
	}

	seen := make(map[string]bool)
	var walk func(string)
	walk = func(path string) {
		report("Walking: ", path)
		if seen[path] {
			fmt.Printf("\tSkipping %s, already seen.\n", path)
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
			fmt.Println("import:", src.ImportPath, i)
			walk(i)
		}
	}
	return srcs
}

func register(rootdir, prefix, kind, arg string, next func(string) *build.Package) func(string) *build.Package {
	dir := cacheDir(rootdir, prefix+kind+"="+arg)
	report("registered:", prefix, "@", arg)
	return func(path string) *build.Package {
		if !strings.HasPrefix(path, prefix) {
			return next(path)
		}
		report("searching", path, "in", prefix, "@", arg)
		dir := filepath.Join(dir, path)
		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			check(err)
		}
		return importPath(path, dir)
	}
}

func importPath(path, dir string) *build.Package {
	report("checking import path for ", path, dir)
	pkg, err := build.ImportDir(dir, 0)
	check(err)
	// ImportDir does not know the import path for this package
	// but we know the prefix, so fix it.
	pkg.ImportPath = path
	return pkg
}

func cacheDir(rootdir, key string) string {
	hash := sha1.Sum([]byte(key))
	return filepath.Join(rootdir, ".golo", "cache", fmt.Sprintf("%x", hash[0:1]), fmt.Sprintf("%x", hash[1:]))
}
