// Command go.onchange automates the compile-restart-test cycle for
// developing go based applications by monitoring for changes in source
// files and dependencies.

// It will:
//     - Install packages.
//     - Restart application.
//     - Run relevant tests.
//     - Clear the screen between cyles to provide a clean log view.
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
		"regexp pattern to match file names against to watch for changes")
	installAll  = flag.Bool("i", true, "install all packages on change")
	runTests    = flag.Bool("t", true, "run tests on change")
	verbose     = flag.Bool("v", false, "verbose")
	clearEnable = flag.Bool("c", true, "clear on restart")
	eventLock   = new(sync.Mutex)

	lastCommandError *tool.CommandError
	process          *os.Process
)

type restartResult int

const (
	restartNecessary restartResult = iota
	restartUnnecessary
	restartBuildFailed
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
	if lastCommandError != nil && string(lastCommandError.StdErr()) == string(commandError.StdErr()) {
		return true
	}
	lastCommandError = commandError
	return false
}

// Compile & Run.
func restart(importPath string, args []string) (result restartResult) {
	if *verbose {
		log.Print("restart requested")
	}
	result = restartBuildFailed
	defer func() {
		if *verbose {
			log.Printf("restart result: %d", result)
		}
	}()
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
		Verbose:     true,
	}
	affected, err := options.Command("build")
	if *verbose {
		log.Printf("Affected: %v", affected)
	}
	defer os.Remove(tempFileName)
	if err != nil {
		if isSameAsLastCommandError(err) {
			if *verbose {
				log.Printf("ignoring same as last command error: %s", err)
			}
			result = restartUnnecessary
			return
		}
		clear()
		log.Print(err)
		result = restartBuildFailed
		return
	}
	if process != nil && len(affected) == 0 {
		if *verbose {
			log.Print("Ignoring rebuild with zero affected packages.")
		}
		result = restartUnnecessary // nothing was changed, don't restart
		return
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
		result = restartBuildFailed
		return
	}
	result = restartNecessary
	return
}

// Install a library package.
func install(importPath string) restartResult {
	options := tool.Options{
		ImportPaths: []string{importPath},
		Verbose:     true,
	}
	affected, err := options.Command("install")
	if *verbose {
		if err != nil {
			log.Printf("Install Error: %v", err)
		} else {
			log.Printf("Install Affected: %v", affected)
		}
	}
	if err == nil && len(affected) == 0 {
		return restartUnnecessary
	}
	return restartNecessary
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

type Monitor struct {
	IncludePattern *regexp.Regexp
	Watcher        *pkgwatcher.Watcher
	ImportPath     string
	Args           []string
}

// Watcher event handler.
func (m *Monitor) event(ev *pkgwatcher.Event) {
	eventLock.Lock()
	defer eventLock.Unlock()
	if filepath.Base(ev.Name)[0] == '.' {
		if *verbose {
			log.Printf("Ignored changed dot file %s", ev.Name)
		}
	} else if m.IncludePattern.Match([]byte(ev.Name)) {
		if *verbose {
			log.Printf("Change triggered restart: %s", ev.Name)
		}
		var installR restartResult
		if *installAll {
			if *verbose {
				log.Printf("Installing all packages.")
			}
			installR = install("all")
		} else {
			installR = install(m.ImportPath)
		}
		if installR == restartUnnecessary {
			if *verbose {
				log.Printf("Skipping because did not install anything.")
			}
			return
		}
		restartR := restart(m.ImportPath, m.Args)
		if restartR == restartUnnecessary {
			return
		}
		go m.Watcher.WatchImportPath(ev.Package.ImportPath, true)
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
	monitor := &Monitor{
		ImportPath:     importPath,
		IncludePattern: re,
		Watcher:        watcher,
		Args:           args,
	}
	restart(importPath, args)
	for {
		if *verbose {
			log.Printf("Main loop iteration.")
		}
		select {
		case ev := <-watcher.Event:
			go monitor.event(ev)
		case err := <-watcher.Error:
			log.Println("error:", err)
		}
	}
}
