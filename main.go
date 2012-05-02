package main

import (
	"strings"
	"time"
	"github.com/howeyc/fsnotify"
	"log"
	"os"
	"os/exec"
	"flag"
	"regexp"
)

var (
	pattern = flag.String("f", ".",
		"The regexp pattern to match file names against to watch for changes.")
	commandStr = flag.String("c", "", "The command to run.")
	verbose = flag.Bool("v", false, "Verbose mode.")

	command string
	commandArgv []string
)

func init() {
	flag.Parse()
	commandArgv = strings.Split(*commandStr, " ")
	if len(commandArgv) == 0 {
		log.Fatalf("A command must be specified.")
	}
	var err error
	command, err = exec.LookPath(commandArgv[0])
	if err != nil {
		log.Fatalf("Error finding binary for %s", *commandStr)
	}
}

func run() (process *os.Process, err error) {
	if *verbose {
		log.Printf("Running command %s with args %v", command, commandArgv)
	}
	return os.StartProcess(command, commandArgv, &os.ProcAttr{
		Files: []*os.File{
			nil,
			os.Stdout,
			os.Stderr,
		},
	})
}

func main() {
	if *verbose {
		log.Printf("Welcome.")
	}
	re, err := regexp.Compile(*pattern)
	if err != nil {
		log.Fatal("Failed to compile given regexp pattern: %s", *pattern)
	}
	pwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Printf("Creating watcher.")
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Fatal(err)
	}
	if *verbose {
		log.Printf("Watching %s.", pwd)
	}
	err = watcher.Watch(pwd)
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
				process.Kill()
				process.Wait()
				time.Sleep(500 * time.Millisecond)
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
