//go:build !darwin

// Package urlevent receives cdswitcher:// links. Linux and Windows start a
// new process for each link, so that process hands the link to the running
// app over a local socket and exits.
package urlevent

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func socketPath(stateDir string) string { return filepath.Join(stateDir, "links.sock") }

// Forward passes link to an already running app and reports whether one took it.
func Forward(stateDir, link string) bool {
	c, err := net.DialTimeout("unix", socketPath(stateDir), 2*time.Second)
	if err != nil {
		return false
	}
	defer c.Close()
	_, err = c.Write([]byte(strings.TrimSpace(link) + "\n"))
	return err == nil
}

// Listen accepts links forwarded by later launches. A socket left behind by a
// crashed run is replaced; if listening fails, the channel never receives.
func Listen(stateDir string) <-chan string {
	out := make(chan string, 8)
	p := socketPath(stateDir)
	_ = os.Remove(p)
	l, err := net.Listen("unix", p)
	if err != nil {
		return out
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
				if line, err := bufio.NewReader(c).ReadString('\n'); err == nil {
					select {
					case out <- strings.TrimSpace(line):
					default: // a burst beyond the buffer is dropped, not queued
					}
				}
			}()
		}
	}()
	return out
}
