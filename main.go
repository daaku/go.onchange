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
	IncludePattern   string
	RunTests         bool
	Verbose          bool
	ClearScreen      bool
	IncludePatternRe *regexp.Regexp
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

// Printf for verbose mode.
func (m *Monitor) Printf(format string, v ...interface{}) {
	if m.Verbose {
		log.Printf(format, v...)
	}
}

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
func (m *Monitor) restart() (result restartResult) {
	m.Printf("restart requested")
	result = restartBuildFailed
	defer m.Printf("restart result: %d", result)
	basename := filepath.Base(m.ImportPath)
	tempFile, err := ioutil.TempFile("", basename+"-")
	if err != nil {
		log.Print("Error creating temp file: %s", err)
		return
	}
	tempFileName := tempFile.Name()
	_ = os.Remove(tempFileName) // the build tool will create this
	options := tool.Options{
		ImportPaths: []string{m.ImportPath},
		Output:      tempFileName,
		Verbose:     true,
	}
	affected, err := options.Command("build")
	m.Printf("Affected: %v", affected)

	defer os.Remove(tempFileName)
	if err != nil {
		if m.isSameAsLastCommandError(err) {
			m.Printf("ignoring same as last command error: %s", err)
			result = restartUnnecessary
			return
		}
		m.clear()
		log.Print(err)
		result = restartBuildFailed
		return
	}
	if m.process != nil && len(affected) == 0 {
		m.Printf("Ignoring rebuild with zero affected packages.")
		result = restartUnnecessary // nothing was changed, don't restart
		return
	}
	m.clear()
	if m.process != nil {
		m.process.Kill()
		m.process.Wait()
		m.process = nil
	}
	m.process, err = os.StartProcess(tempFileName, m.Args, &os.ProcAttr{
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
	if err != nil {
		m.Printf("Install Error: %v", err)
	} else {
		m.Printf("Install Affected: %v", affected)
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

// Check if a file change should be ignored.
func (m *Monitor) ShouldIgnore(name string) bool {
	if filepath.Base(name)[0] == '.' {
		m.Printf("Ignored changed dot file %s", name)
		return true
	} else if m.IncludePatternRe.Match([]byte(name)) {
		return false
	}

	m.Printf("Ignored changed file %s", name)
	return true
}

// Watcher event handler.
func (m *Monitor) event(ev *pkgwatcher.Event) {
	if m.ShouldIgnore(ev.Name) {
		return
	}

	m.eventLock.Lock()
	defer m.eventLock.Unlock()
	m.Printf("Change triggered restart: %s", ev.Name)
	var installR restartResult
	m.Printf("Installing all packages.")
	installR = m.install("all")
	if installR == restartUnnecessary {
		m.Printf("Skipping because did not install anything.")
		return
	}
	restartR := m.restart()
	if restartR == restartUnnecessary {
		return
	}
	go m.Watcher.WatchImportPath(ev.Package.ImportPath, true)
	if m.RunTests {
		m.Printf("Testing %s.", ev.Package.ImportPath)
		m.test(ev.Package.ImportPath)
	}
}

// Start the main blocking Monitor loop.
func (m *Monitor) Start() {
	m.restart()
	for {
		m.Printf("Main loop iteration.")
		select {
		case ev := <-m.Watcher.Event:
			go m.event(ev)
		case err := <-m.Watcher.Error:
			log.Println("watcher error:", err)
		}
	}
}

func main() {
	monitor := &Monitor{
		eventLock: new(sync.Mutex),
	}
	flag.StringVar(&monitor.IncludePattern, "f", ".",
		"regexp pattern to match file names against to watch for changes")
	flag.BoolVar(&monitor.RunTests, "t", true, "run tests on change")
	flag.BoolVar(&monitor.Verbose, "v", false, "verbose")
	flag.BoolVar(&monitor.ClearScreen, "c", true, "clear screen on restart")
	flag.Parse()

	monitor.ImportPath = flag.Arg(0)
	monitor.Args = append(
		[]string{filepath.Base(monitor.ImportPath)},
		flag.Args()[1:flag.NArg()]...)

	re, err := regexp.Compile(monitor.IncludePattern)
	if err != nil {
		log.Fatal("Failed to compile given regexp pattern: %s", monitor.IncludePattern)
	}
	monitor.IncludePatternRe = re

	watcher, err := pkgwatcher.NewWatcher([]string{monitor.ImportPath}, "")
	if err != nil {
		log.Fatal(err)
	}
	monitor.Watcher = watcher

	monitor.Start()
}
