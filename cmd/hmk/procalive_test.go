package main

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

// A child that has exited but isn't reaped yet is a zombie: not alive.
func TestProcessAliveZombie(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("this process isn't alive")
	}
	c := exec.Command("/bin/sh", "-c", "exit 0")
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for processAlive(c.Process.Pid) {
		if time.Now().After(deadline) {
			t.Fatal("an exited, unreaped child counts as alive")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
