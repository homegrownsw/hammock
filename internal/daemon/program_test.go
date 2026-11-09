package daemon

import (
	"os"
	"os/exec"
	"slices"
	"testing"
	"time"

	"github.com/creack/pty"
)

func TestProgramNames(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"vim", []string{"vim", "main.go"}, []string{"vim"}},
		{"-zsh", []string{"-zsh"}, []string{"zsh"}},
		{"htop", nil, []string{"htop"}},
		{"Python", []string{"/usr/bin/python3", "manage.py", "runserver"}, []string{"manage", "python"}},
		{"python3.12", []string{"python3.12", "-m", "pytest", "-x"}, []string{"pytest", "python"}},
		{"python3", []string{"python3", "-c", "print(1)"}, []string{"python"}},
		{"python3", []string{"python3"}, []string{"python"}},
		{"node", []string{"node", "/opt/homebrew/bin/npx", "vite", "--port", "3000"}, []string{"vite", "npx", "node"}},
		{"node", []string{"node", "/repo/node_modules/.bin/vitest", "run"}, []string{"vitest", "node"}},
		{"node", []string{"node", "/repo/node_modules/vite/bin/vite.js"}, []string{"vite", "node"}},
		{"node", []string{"node", "/usr/lib/node_modules/@anthropic-ai/claude-code/cli.js"}, []string{"claude", "node"}},
		{"node", []string{"node", "-r", "ts-node/register", "server.ts"}, []string{"server", "node"}},
		// A package.json script isn't a program: npm stands for itself.
		{"node", []string{"npm run dev"}, []string{"npm", "node"}},
		{"node", []string{"npm run next"}, []string{"npm", "node"}},
		{"node", []string{"node", "/opt/homebrew/lib/node_modules/npm/bin/npm-cli.js", "test"}, []string{"npm", "node"}},
		{"node", []string{"node", "/opt/homebrew/bin/pnpm", "exec", "vitest"}, []string{"vitest", "pnpm", "node"}},
		{"bun", []string{"bun", "run", "dev"}, []string{"bun"}},
		{"bun", []string{"bun", "run", "server.ts"}, []string{"server", "bun"}},
		{"python3", []string{"python3", "-I", "app.py"}, []string{"app", "python"}},
		{"uv", []string{"uv", "run", "-p", "3.12", "x.py"}, []string{"x", "uv"}},
		{"caffeinate", []string{"caffeinate", "-i", "make", "test"}, []string{"make", "caffeinate"}},
		{"env", []string{"env", "DEBUG=1", "cargo", "run"}, []string{"cargo", "env"}},
		{"node", []string{"node", "."}, []string{"node"}},
		// Commands that run a command show what they run.
		{"sudo", []string{"sudo", "-u", "root", "nvim", "/etc/hosts"}, []string{"nvim", "sudo"}},
		{"watch", []string{"watch", "-n", "2", "kubectl", "get", "pods"}, []string{"kubectl", "watch"}},
		{"timeout", []string{"timeout", "-s", "INT", "30s", "go", "test", "./..."}, []string{"go", "timeout"}},
		{"op", []string{"op", "run", "--", "terraform", "plan"}, []string{"terraform", "op"}},
		{"uv", []string{"uv", "run", "python", "app.py"}, []string{"app", "python", "uv"}},
		{"uv", []string{"uv", "run", "--with", "rich", "pytest"}, []string{"pytest", "uv"}},
		{"ruby", []string{"ruby", "/gems/bin/bundle", "exec", "rspec"}, []string{"rspec", "bundle", "ruby"}},
		{"bun", []string{"bun", "x", "vite"}, []string{"vite", "bun"}},
		{"deno", []string{"deno", "run", "-A", "main.ts"}, []string{"main", "deno"}},
		// Shells: a script names the program, otherwise the shell does.
		{"bash", []string{"/bin/bash", "-p", "/opt/homebrew/Library/Homebrew/brew.sh", "update", "--auto-update"}, []string{"brew", "bash"}},
		{"ruby", []string{"/opt/homebrew/Library/Homebrew/vendor/portable-ruby/current/bin/ruby", "-W1", "--disable=gems,rubyopt", "/opt/homebrew/Library/Homebrew/brew.rb", "outdated"}, []string{"brew", "ruby"}},
		{"bash", []string{"bash"}, []string{"bash"}},
		{"bash", []string{"bash", "--login", "-i"}, []string{"bash"}},
		{"zsh", []string{"zsh", "-c", "make test"}, []string{"zsh"}},
		{"bash", []string{"bash", "-o", "pipefail", "deploy.sh"}, []string{"deploy", "bash"}},
		{"sh", []string{"sh", "./install"}, []string{"install", "sh"}},
	}
	for _, c := range cases {
		if got := programNames(c.name, c.args); !slices.Equal(got, c.want) {
			t.Errorf("programNames(%q, %q) = %q, want %q", c.name, c.args, got, c.want)
		}
	}
}

// A real terminal: the shell at its prompt, then a job in front of it.
func TestForegroundProgram(t *testing.T) {
	sh := exec.Command("/bin/bash", "--norc", "--noprofile", "-i")
	sh.Env = append(os.Environ(), "PS1=$ ")
	f, err := pty.Start(sh)
	if err != nil {
		t.Skip("no pty:", err)
	}
	defer func() { sh.Process.Kill(); sh.Wait(); f.Close() }()
	go func() {
		b := make([]byte, 4096)
		for {
			if _, err := f.Read(b); err != nil {
				return
			}
		}
	}()

	waitFor := func(want string) {
		t.Helper()
		var got []string
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(50 * time.Millisecond) {
			if got = foregroundProgram(sh.Process.Pid); len(got) > 0 && got[0] == want {
				return
			}
		}
		t.Fatalf("foreground = %q, want %s first", got, want)
	}
	waitFor("bash")
	f.Write([]byte("sleep 30\n"))
	waitFor("sleep")
	f.Write([]byte{3}) // ^C
	waitFor("bash")
	// A background job isn't in front.
	f.Write([]byte("sleep 2 &\n")) // short: a killed shell leaves it behind
	time.Sleep(300 * time.Millisecond)
	waitFor("bash")
}
