package holder

import (
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

// BenchmarkHolderThroughput measures PTY → holder → client bytes/s, the
// ceiling for everything downstream.
func BenchmarkHolderThroughput(b *testing.B) {
	const size = 32 << 20
	b.SetBytes(size)
	for i := 0; i < b.N; i++ {
		_, sock := startHolderB(b, "/bin/sh", "-c", "head -c 33554432 /dev/zero | tr '\\0' 'a'")
		c, err := Dial(sock, 0, nil)
		if err != nil {
			b.Fatal(err)
		}
		start := time.Now()
		got := 0
		frames := 0
		for got < size {
			ev, err := c.Next()
			if err != nil {
				b.Fatal(err)
			}
			got += len(ev.Output)
			frames++
		}
		b.ReportMetric(float64(got)/float64(frames), "bytes/frame")
		_ = start
		c.Close()
	}
}

// BenchmarkRawPTY is the OS baseline: a bare read loop on the PTY master.
func BenchmarkRawPTY(b *testing.B) {
	const size = 32 << 20
	b.SetBytes(size)
	for i := 0; i < b.N; i++ {
		cmd := exec.Command("/bin/sh", "-c", "head -c 33554432 /dev/zero | tr '\\0' 'a'")
		f, err := pty.Start(cmd)
		if err != nil {
			b.Fatal(err)
		}
		buf := make([]byte, 64<<10)
		got := 0
		for got < size {
			n, err := f.Read(buf)
			if err != nil {
				break
			}
			got += n
		}
		f.Close()
		cmd.Wait()
	}
}

// BenchmarkRawPTYBlocking reads with blocking syscalls, bypassing Go's poller.
func BenchmarkRawPTYBlocking(b *testing.B) {
	const size = 32 << 20
	b.SetBytes(size)
	for i := 0; i < b.N; i++ {
		cmd := exec.Command("/bin/sh", "-c", "head -c 33554432 /dev/zero | tr '\\0' 'a'")
		f, err := pty.Start(cmd)
		if err != nil {
			b.Fatal(err)
		}
		fd := int(f.Fd()) // Fd() switches the file to blocking mode
		buf := make([]byte, 64<<10)
		got := 0
		for got < size {
			n, err := syscall.Read(fd, buf)
			if err != nil || n <= 0 {
				break
			}
			got += n
		}
		f.Close()
		cmd.Wait()
	}
}
