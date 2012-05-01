package main

import (
	"log"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
    "strconv"
)

const (
	SYNC_IDLE_SECS = 5
)

type GitBackend struct {
	gitPath string

	rootDir string

	syncLock      sync.Mutex
	isSyncPending bool
    isSyncActive bool   // Ignore all events during sync

    lastEvent time.Time

	pushHook func()
}

func NewGitBackend(config *Config) *GitBackend {

	rootDir := config.syncDir

	gitPath, err := exec.LookPath("git")
	if err != nil {
		log.Fatal("Error looking for 'git' on path. ", err)
	}

	return &GitBackend{rootDir: rootDir, gitPath: gitPath}
}

// A file or directory has been created
func (self *GitBackend) Changed(filename string) {
	if self.isGit(filename) || self.isSyncActive {
		return
	}
    self.lastEvent = time.Now()
	go self.syncLater()
}

// Run: git pull; git add --all ; git commit --all; git push
func (self *GitBackend) Sync() error {

    log.Println("* Sync start")
    self.isSyncActive = true

	var err *GitError

	// Pull first to ensure a fast-forward when we push
	err = self.pull()
	if err != nil {
        self.isSyncActive = false
		return err
	}

	err = self.git("add", "--all")
	if err != nil {
        self.isSyncActive = false
		return err
	}

    self.displayStatus("status", "--porcelain")

	err = self.git("commit", "--all", "--message=loftus")
	if err != nil {
        // An err with status==1 means nothing to commit,
        // that counts as a clean exit
        self.isSyncActive = false
        log.Println("* Sync end")
		return err
	}

	err = self.push()
	if err != nil {
        self.isSyncActive = false
        return err
	}

    self.isSyncActive = false
    log.Println("* Sync end")
	return nil
}

//Display summary of changes
func (self *GitBackend) displayStatus(args ...string) {

    created, modified, deleted := self.status(args...)

    var msg string
    if len(created) == 1 {
        msg += "New: " + created[0]
    } else if len(created) > 1 {
        msg += "New: " + strconv.Itoa(len(created))
    }

    if len(modified) == 1 {
        msg += " Edit: " + modified[0]
    } else if len(modified) > 1 {
        msg += " Edit: " + strconv.Itoa(len(modified))
    }

    if len(deleted) == 1 {
        msg += " Del: " + deleted[0]
    } else if len(deleted) > 1 {
        msg += " Del: " + strconv.Itoa(len(deleted))
    }

    if len(msg) != 0 {
        Info(msg)
    }
}

// Register the function to be called after we push to remote
func (self *GitBackend) RegisterPushHook(callback func()) {
	self.pushHook = callback
}

// Should the inotify watch watch the given path
func (self *GitBackend) ShouldWatch(filename string) bool {
	return !self.isGit(filename)
}

// Status of directory. Returns filenames created, modified or deleted.
func (self *GitBackend) status(args ...string) (created []string, modified []string, deleted []string) {

	cmd := exec.Command(self.gitPath, args...)
	log.Println(strings.Join(cmd.Args, " "))

	cmd.Dir = self.rootDir

	output, err := cmd.CombinedOutput()
    if err != nil {
        log.Println(err)
    }
    if len(output) > 0 {
        log.Println(string(output))
    }

    for _, line := range strings.Split(string(output), "\n") {
        if len(line) == 0 {
            continue
        }

        // Replace double spaces and tabs with single space so that Split is predictable
        line = strings.Replace(line, "  ", " ", -1)
        line = strings.Replace(line, "\t", " ", -1)

        lineParts := strings.Split(line, " ")

        status := lineParts[0]
        filename := lineParts[1]

        switch status[0] {
            case 'A':
                created = append(created, filename)
            case 'M':
                modified = append(modified, filename)
            case 'R':                         // Renamed, but treat as Modified
                modified = append(modified, filename)
            case 'D':
                deleted = append(deleted, filename)
            case '?':
                log.Println("Unknown. Need git add", filename)
            default:
                log.Println("Other", status)
        }
    }

    return created, modified, deleted
}

// Is filename inside a .git directory?
func (self *GitBackend) isGit(filename string) bool {
	return strings.Contains(filename, ".git")
}

// Schedule a synchronise for in a few seconds. Run it in go routine.
func (self *GitBackend) syncLater() {

	// ensure only once per time - might be able to use sync.Once instead (?)
	self.syncLock.Lock()
	if self.isSyncPending {
		self.syncLock.Unlock()
		return
	}
	self.isSyncPending = true
	self.syncLock.Unlock()

    for time.Now().Sub(self.lastEvent) < (SYNC_IDLE_SECS * time.Second) {
        time.Sleep(time.Second)
    }

    log.Println("syncLater initiated sync")
	self.Sync()

	self.isSyncPending = false
}

// Run: git push
func (self *GitBackend) push() *GitError {
	err := self.git("push")
	if err == nil && self.pushHook != nil {
		go self.pushHook()
	}
	return err
}

// Run: git pull
func (self *GitBackend) pull() *GitError {

    var err *GitError

    err = self.git("fetch")
    if err != nil {
        return err
    }

    self.displayStatus("diff", "origin/master", "--name-status")
	err = self.git("merge", "origin/master")
	return err
}

/* Runs a git command, returns nil if success, error if err
   Errors are not always bad. For example a "commit" that
   didn't have to do anything returns an error.
*/
func (self *GitBackend) git(gitCmd string, args ...string) *GitError {

	cmd := exec.Command(self.gitPath, append([]string{gitCmd}, args...)...)
	cmd.Dir = self.rootDir
	log.Println(strings.Join(cmd.Args, " "))

	output, err := cmd.CombinedOutput()
    if len(output) > 0 {
        log.Println(string(output))
    }

	if err == nil {
        return nil
    }

    exitStatus := err.(*exec.ExitError).Sys().(syscall.WaitStatus).ExitStatus()
    gitErr := &GitError{
        cmd: strings.Join(cmd.Args, " "),
        internalError: err,
        output: string(output),
        status: exitStatus}
    if exitStatus != 1 {            // 1 means command had nothing to do
        log.Println(err)
    }
    return gitErr
}

type GitError struct {
	cmd           string
	internalError error
	output        string
    status        int
}

// error implementation which displays git info
func (self *GitError) Error() string {
    msg := "git error running: " + self.cmd + "\n\n"
    msg += self.output + "\n"
	msg += self.internalError.Error()
    return msg
}
