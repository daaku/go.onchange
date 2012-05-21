// Command go.onchange automates the compile-restart cycle for developing
// go based servers by monitoring for changes in source files and
// dependencies. It can additionally also install packages as they
// change in order to allow the gocode autocomplete daemon to be able
// to find upto date information.
package main

import (
	"flag"
	"github.com/nshah/go.pkgwatcher"
	"github.com/nshah/go.tool"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
)

var (
	pattern = flag.String("f", ".",
		"The regexp pattern to match file names against to watch for changes.")
	installDeps = flag.Bool("i", true, "Install changed library packages.")
	runTests    = flag.Bool("t", true, "Run tests for changed packages.")
	verbose     = flag.Bool("v", false, "Verbose mode.")

	process *os.Process
)

// Compile & Run.
func restart(importPath string, args []string) {
	basename := filepath.Base(importPath)
	tempFile, err := ioutil.TempFile("", basename+"-")
	if err != nil {
		log.Print("Error creating temp file: %s", err)
		return
	}
	tempFileName := tempFile.Name()
	_ = os.Remove(tempFileName) // the build tool will create this
	options := tool.Options{
		ImportPaths: []string{importPath},
		Output:      tempFileName,
	}
	affected, err := options.Command("build")
	defer os.Remove(tempFileName)
	if err != nil {
		log.Print(err)
		return
	}
	if len(affected) == 0 {
		if *verbose {
			log.Print("Ignoring rebuild with zero affected packages.")
		}
		return // nothing was changed, don't restart
	}
	if process != nil {
		process.Kill()
		process.Wait()
		process = nil
	}
	process, err = os.StartProcess(tempFileName, args, &os.ProcAttr{
		Files: []*os.File{
			nil,
			os.Stdout,
			os.Stderr,
		},
	})
	if err != nil {
		log.Printf("Failed to run command: %s", err)
	}
}

// Install a library package.
func install(importPath string) {
	options := tool.Options{
		ImportPaths: []string{importPath},
	}
	_, err := options.Command("install")
	// only output in verbose since restart() will most likely echo this too.
	if *verbose && err != nil {
		log.Print(err)
	}
}

// Test a package.
func test(importPath string) {
	options := tool.Options{
		ImportPaths: []string{importPath},
	}
	_, err := options.Command("test")
	if err != nil {
		log.Print(err)
	}
}

func main() {
	flag.Parse()
	importPath := flag.Arg(0)
	args := append(
		[]string{filepath.Base(importPath)}, flag.Args()[1:flag.NArg()]...)

	re, err := regexp.Compile(*pattern)
	if err != nil {
		log.Fatal("Failed to compile given regexp pattern: %s", *pattern)
	}
	watcher, err := pkgwatcher.NewWatcher([]string{importPath}, "")
	if err != nil {
		log.Fatal(err)
	}
	restart(importPath, args)
	for {
		select {
		case ev := <-watcher.Event:
			if filepath.Base(ev.Name)[0] == '.' {
				if *verbose {
					log.Printf("Ignored changed dot file %s", ev.Name)
				}
			} else if re.Match([]byte(ev.Name)) {
				if *verbose {
					log.Printf("Change triggered restart: %s", ev.Name)
				}
				restart(importPath, args)
				if *installDeps && !ev.Package.IsCommand() {
					if *verbose {
						log.Printf("Installing changed package: %s", ev.Package.ImportPath)
					}
					install(ev.Package.ImportPath)
				}
				if *runTests {
					test(ev.Package.ImportPath)
				}
			} else {
				if *verbose {
					log.Printf("Ignored changed file %s", ev.Name)
				}
			}
		case err := <-watcher.Error:
			log.Println("error:", err)
		}
	}
}
