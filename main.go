// Command go.onchange automates the compile-restart cycle for developing
// go based servers by monitoring for changes in source files and
// dependencies. It can additionally also install packages as they
// change in order to allow the gocode autocomplete daemon to be able
// to find upto date information.
package main

import (
	"flag"
	"fmt"
	"github.com/daaku/go.pkgwatcher"
	"github.com/daaku/go.tool"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
)

var (
	pattern = flag.String("f", ".",
		"The regexp pattern to match file names against to watch for changes.")
	installAll  = flag.Bool("i", true, "Install packages on change.")
	runTests    = flag.Bool("t", true, "Run tests on change.")
	verbose     = flag.Bool("v", false, "Verbose mode.")
	clearEnable = flag.Bool("c", true, "Clear display on restart.")
	restartLock = new(sync.Mutex)

	lastCommandError *tool.CommandError
	process          *os.Process
)

func clear() {
	if *clearEnable {
		fmt.Printf("\033[2J")
		fmt.Printf("\033[H")
	}
}

// Checks and also updates the last command.
func isSameAsLastCommandError(err error) bool {
	commandError, ok := err.(*tool.CommandError)
	if !ok {
		return false
	}
	if lastCommandError != nil && lastCommandError.StdErr().String() == commandError.StdErr().String() {
		return true
	}
	lastCommandError = commandError
	return false
}

// Compile & Run.
func restart(importPath string, args []string) {
	restartLock.Lock()
	defer restartLock.Unlock()
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
		if isSameAsLastCommandError(err) {
			return
		}
		clear()
		log.Print(err)
		return
	}
	if len(affected) == 0 {
		if *verbose {
			log.Print("Ignoring rebuild with zero affected packages.")
		}
		return // nothing was changed, don't restart
	}
	clear()
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
		return
	}
}

// Install a library package.
func install(importPath string) {
	options := tool.Options{
		ImportPaths: []string{importPath},
	}
	options.Command("install")
}

// Test a package.
func test(importPath string) {
	options := tool.Options{
		ImportPaths: []string{importPath},
	}
	_, err := options.Command("test")
	if err != nil && !isSameAsLastCommandError(err) {
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
		if *verbose {
			log.Printf("Main loop iteration.")
		}
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
				go watcher.WatchImportPath(ev.Package.ImportPath, true)
				if *installAll {
					if *verbose {
						log.Printf("Installing all packages.")
					}
					install("all")
				}
				if *runTests {
					if *verbose {
						log.Printf("Testing %s.", ev.Package.ImportPath)
					}
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
