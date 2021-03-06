package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestMainLoop(t *testing.T) {

	config := &Config{
		isServer:   false,
		serverAddr: "test.local",
		syncDir:    "/tmp/fake"}

	external := &MockExternal{}
	backend := NewGitBackend(config, external)

	watchChannel := make(chan Event)
	incomingChannel := make(chan string)

	client := Client{
		backend:  backend,
		watch:    watchChannel,
		external: external,
		incoming: incomingChannel,
		isOnline: true,
	}

	go client.run()

	// Something changed.
	watchChannel <- Event{"/tmp/fake/one.txt", "Edit"}

	expected := []string{
		"/usr/bin/git remote show origin",
		"/usr/bin/git fetch",
		"/usr/bin/git merge origin/master",
		"/usr/bin/git add --all",
		"/usr/bin/git commit --all --message=Startup sync",
		"/usr/bin/git push",
	}
	if fmt.Sprintf("%v", external.cmds) != fmt.Sprintf("%v", expected) {
		t.Error("Unexpected exec: ", external.cmds)
	}
}

type MockExternal struct {
	cmds []string
}

func (self *MockExternal) Exec(rootDir string, cmd string, args ...string) ([]byte, error) {

	self.cmds = append(self.cmds, cmd+" "+strings.Join(args, " "))
	return []byte(""), nil
}
