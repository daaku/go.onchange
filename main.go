// Command go.onchange automates the compile-restart-test cycle for
// developing go based applications by monitoring for changes in source
// files and dependencies.
//
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

// The main Monitor instance.
type Monitor struct {
	WatchPattern     string
	RunTests         bool
	Verbose          bool
	ClearScreen      bool
	IncludePattern   *regexp.Regexp
	Watcher          *pkgwatcher.Watcher
	ImportPath       string
	Args             []string
	eventLock        sync.Locker
	lastCommandError *tool.CommandError
	process          *os.Process
}

type restartResult int

const (
	restartNecessary restartResult = iota
	restartUnnecessary
	restartBuildFailed
)

func (m *Monitor) clear() {
	if m.ClearScreen {
		fmt.Printf("\033[2J")
		fmt.Printf("\033[H")
	}
}

// Checks and also updates the last command.
func (m *Monitor) isSameAsLastCommandError(err error) bool {
	commandError, ok := err.(*tool.CommandError)
	if !ok {
		return false
	}
	last := m.lastCommandError
	if last != nil && string(last.StdErr()) == string(commandError.StdErr()) {
		return true
	}
	m.lastCommandError = commandError
	return false
}

// Compile & Run.
func (m *Monitor) restart(importPath string, args []string) (result restartResult) {
	if m.Verbose {
		log.Print("restart requested")
	}
	result = restartBuildFailed
	defer func() {
		if m.Verbose {
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
	if m.Verbose {
		log.Printf("Affected: %v", affected)
	}
	defer os.Remove(tempFileName)
	if err != nil {
		if m.isSameAsLastCommandError(err) {
			if m.Verbose {
				log.Printf("ignoring same as last command error: %s", err)
			}
			result = restartUnnecessary
			return
		}
		m.clear()
		log.Print(err)
		result = restartBuildFailed
		return
	}
	if m.process != nil && len(affected) == 0 {
		if m.Verbose {
			log.Print("Ignoring rebuild with zero affected packages.")
		}
		result = restartUnnecessary // nothing was changed, don't restart
		return
	}
	m.clear()
	if m.process != nil {
		m.process.Kill()
		m.process.Wait()
		m.process = nil
	}
	m.process, err = os.StartProcess(tempFileName, args, &os.ProcAttr{
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
func (m *Monitor) install(importPath string) restartResult {
	options := tool.Options{
		ImportPaths: []string{importPath},
		Verbose:     true,
	}
	affected, err := options.Command("install")
	if m.Verbose {
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
func (m *Monitor) test(importPath string) {
	options := tool.Options{
		ImportPaths: []string{importPath},
	}
	_, err := options.Command("test")
	if err != nil && !m.isSameAsLastCommandError(err) {
		log.Print(err)
	}
}

// Watcher event handler.
func (m *Monitor) event(ev *pkgwatcher.Event) {
	m.eventLock.Lock()
	defer m.eventLock.Unlock()
	if filepath.Base(ev.Name)[0] == '.' {
		if m.Verbose {
			log.Printf("Ignored changed dot file %s", ev.Name)
		}
	} else if m.IncludePattern.Match([]byte(ev.Name)) {
		if m.Verbose {
			log.Printf("Change triggered restart: %s", ev.Name)
		}
		var installR restartResult
		if m.Verbose {
			log.Printf("Installing all packages.")
		}
		installR = m.install("all")
		if installR == restartUnnecessary {
			if m.Verbose {
				log.Printf("Skipping because did not install anything.")
			}
			return
		}
		restartR := m.restart(m.ImportPath, m.Args)
		if restartR == restartUnnecessary {
			return
		}
		go m.Watcher.WatchImportPath(ev.Package.ImportPath, true)
		if m.RunTests {
			if m.Verbose {
				log.Printf("Testing %s.", ev.Package.ImportPath)
			}
			m.test(ev.Package.ImportPath)
		}
	} else {
		if m.Verbose {
			log.Printf("Ignored changed file %s", ev.Name)
		}
	}
}

func main() {
	monitor := &Monitor{
		eventLock: new(sync.Mutex),
	}
	flag.StringVar(&monitor.WatchPattern, "f", ".",
		"regexp pattern to match file names against to watch for changes")
	flag.BoolVar(&monitor.RunTests, "t", true, "run tests on change")
	flag.BoolVar(&monitor.Verbose, "v", false, "verbose")
	flag.BoolVar(&monitor.ClearScreen, "c", true, "clear screen on restart")
	flag.Parse()

	monitor.ImportPath = flag.Arg(0)
	args := append(
		[]string{filepath.Base(monitor.ImportPath)},
		flag.Args()[1:flag.NArg()]...)

	re, err := regexp.Compile(monitor.WatchPattern)
	if err != nil {
		log.Fatal("Failed to compile given regexp pattern: %s", monitor.Watcher)
	}
	watcher, err := pkgwatcher.NewWatcher([]string{monitor.ImportPath}, "")
	if err != nil {
		log.Fatal(err)
	}

	monitor.IncludePattern = re
	monitor.Watcher = watcher
	monitor.Args = args
	monitor.restart(monitor.ImportPath, args)
	for {
		if monitor.Verbose {
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
