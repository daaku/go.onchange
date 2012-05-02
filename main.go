package main

import (
	"flag"
	"fmt"
	"github.com/howeyc/fsnotify"
	"go/build"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
)

var (
	pattern = flag.String("f", ".",
		"The regexp pattern to match file names against to watch for changes.")
	verbose = flag.Bool("v", false, "Verbose mode.")

	pkg         *build.Package
	goBin       string
	commandBin  string
	process     *os.Process
	commandArgv []string
)

func init() {
	flag.Parse()
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "/"
	}
	pkg, err = build.Import(flag.Arg(0), cwd, build.FindOnly)
	if err != nil {
		log.Fatalf("Can't load package: %s", err)
	}
	goBin, err = exec.LookPath("go")
	if err != nil {
		log.Fatalf("Error finding go binary: %s", err)
	}
}

func basename() string {
	return filepath.Base(pkg.ImportPath)
}

func compile() error {
	tempFile, err := ioutil.TempFile("", basename() + "-")
	if err != nil {
		return err
	}
	_ = os.Remove(tempFile.Name()) // the build tool will create this
	cmd := exec.Command(goBin, "build", "-o", tempFile.Name(), pkg.ImportPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf(
			"Failed to build package %s: %s", pkg.ImportPath, string(out))
	}
	commandBin = tempFile.Name()
	return nil
}

func run() (process *os.Process, err error) {
	if *verbose {
		log.Printf("Running command %s with args %v", commandBin, commandArgv)
	}
	argsWithName := []string{basename()}
	argsWithName = append(argsWithName, commandArgv...)
	p, e := os.StartProcess(commandBin, argsWithName, &os.ProcAttr{
		Files: []*os.File{
			nil,
			os.Stdout,
			os.Stderr,
		},
	})
	_ = os.Remove(commandBin)
	return p, e
}

func main() {
	if *verbose {
		log.Printf("Welcome.")
	}
	re, err := regexp.Compile(*pattern)
	if err != nil {
		log.Fatal("Failed to compile given regexp pattern: %s", *pattern)
	}
	if *verbose {
		log.Printf("Creating watcher.")
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Printf("Watching %s.", pkg.Dir)
	}
	err = watcher.Watch(pkg.Dir)
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Printf("About to compile.")
	}
	err = compile()
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Printf("About to run command.")
	}
	process, err := run()
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Printf("Waiting for changes with pattern: %s\n", *pattern)
	}
	for {
		select {
		case ev := <-watcher.Event:
			if *verbose {
				log.Println("event:", ev)
			}
			if re.Match([]byte(ev.Name)) {
				if *verbose {
					log.Println("Change triggered restart.")
				}
				err = compile()
				if err != nil {
					log.Println("Ignoring failed compile: %s", err)
					continue
				}
				process.Kill()
				process.Wait()
				process, err = run()
				if err != nil {
					log.Fatal(err)
				}
			}
		case err := <-watcher.Error:
			log.Println("error:", err)
		}
	}
}
