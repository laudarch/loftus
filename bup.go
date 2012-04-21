package main

import (
	"exp/inotify"
	"flag"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	INTERESTING = inotify.IN_MODIFY | inotify.IN_CREATE | inotify.IN_DELETE | inotify.IN_MOVE
)

type Backend interface {
	Sync() error
	Changed(filename string)
	ShouldWatch(filename string) bool
	RegisterPushHook(func())
}

type Config struct {
	isServer   bool
	isCheck    bool
	serverAddr string
	syncDir    string
	logDir     string
	stdout     bool
}

type Client struct {
	backend  Backend
	rootDir  string
	watcher  *inotify.Watcher
	logger   *log.Logger
	incoming chan string
}

func main() {

	config := confFromFlags()
	log.Println("Logging to ", config.logDir)

	os.Mkdir(config.logDir, 0750)

	if config.isCheck {
		runCheck(config)

	} else if config.isServer {
		startServer(config)

	} else {
        // No point making the sync dir, it needs to be a repo
	    //os.Mkdir(config.syncDir, 0750)
		startClient(config)
	}
}

// Parse commands line flags in to a configuration object
func confFromFlags() *Config {

	defaultSync := os.Getenv("HOME") + "/bup/"
	var syncDir = flag.String(
		"dir",
		defaultSync,
		"Synchronise this directory. Must already be a git repo with a remote (i.e. 'git pull' works)")

	var isServer = flag.Bool("server", false, "Be the server")
	var serverAddr = flag.String(
		"address",
		"127.0.0.1:8007",
		"address:post where server is listening")

	var isCheck = flag.Bool("check", false, "Check we are setup correctly")

	defaultLog := os.Getenv("HOME") + "/.bup/"
	var logDir = flag.String("log", defaultLog, "Log directory")

	var stdout = flag.Bool("stdout", false, "Log to stdout")

	flag.Parse()

	return &Config{
		isServer:   *isServer,
		isCheck:    *isCheck,
		serverAddr: *serverAddr,
		syncDir:    *syncDir,
		logDir:     *logDir,
		stdout:     *stdout}
}

// Watch directories, called sync methods on backend, etc
func startClient(config *Config) {

	syncDir := config.syncDir

	logger := openLog(config, "client.log")

	logger.Println("Synchronising: ", syncDir)

	syncDir = strings.TrimRight(syncDir, "/")
	backend := NewGitBackend(config)

	watcher, _ := inotify.NewWatcher()

	incomingChannel := make(chan string)

	client := Client{
		rootDir:  syncDir,
		backend:  backend,
		watcher:  watcher,
		logger:   logger,
		incoming: incomingChannel,
	}
	client.addWatches()

	// Always start with a sync to bring us up to date
	err := backend.Sync()
	if err != nil && err.(*GitError).status != 1 {
		Warn(err.Error())
	}

	go udpListen(logger, incomingChannel)
	go tcpListen(logger, config.serverAddr, incomingChannel)
	client.run()
}

func openLog(config *Config, name string) *log.Logger {

	if config.stdout {
		return log.New(os.Stdout, "", log.LstdFlags)
	}

	writer, err := os.OpenFile(
		config.logDir+name, os.O_RDWR|os.O_APPEND|os.O_CREATE, 0650)

	if err != nil {
		log.Fatal("Error opening log file", name, " in ", config.logDir, err)
	}

	return log.New(writer, "", log.LstdFlags)
}

// Main loop
func (self *Client) run() {

	// push hook will be called from a go routine
	self.backend.RegisterPushHook(func() {
		msg := "Updated\n"
		if remoteConn != nil { // remoteConn is global in comms.go
			tcpSend(remoteConn, msg)
		}
		udpSend(self.logger, msg)
	})

	for {
		select {
		case ev := <-self.watcher.Event:

			self.logger.Println(ev)

			isCreate := ev.Mask&inotify.IN_CREATE != 0
			isDir := ev.Mask&inotify.IN_ISDIR != 0

			if isCreate && isDir && self.backend.ShouldWatch(ev.Name) {
				self.logger.Println("Adding watch", ev.Name)
				self.watcher.AddWatch(ev.Name, INTERESTING)
			}

            self.logger.Println("Calling Changed")
			self.backend.Changed(ev.Name)

		case err := <-self.watcher.Error:
			self.logger.Println("error:", err)

		case <-self.incoming:
			self.logger.Println("Remote update notification")
			self.backend.Sync()
		}

	}
}

// Add inotify watches on rootDir and all sub-dirs
func (self *Client) addWatches() {

	addSingleWatch := func(path string, info os.FileInfo, err error) error {
		if info.IsDir() && self.backend.ShouldWatch(path) {
			self.logger.Println("Watching", path)
			self.watcher.AddWatch(path, INTERESTING)
		}
		return nil
	}

	err := filepath.Walk(self.rootDir, addSingleWatch)
	if err != nil {
		self.logger.Fatal(err)
	}
}

// Utility function to inform user about something - for example file changes
func Info(msg string) {
	cmd := exec.Command("bup_info", msg)
	cmd.Run()
}

// Utility function to warn user about something - for example a git error
func Warn(msg string) {
	cmd := exec.Command("bup_alert", msg)
	cmd.Run()
}
