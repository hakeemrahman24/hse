package mcusubmit

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"time"
)

// Sandbox runs the PDF converter with as little power as possible. Ghostscript parses a file that
// came from the internet, so we assume that one day a bug lets a crafted PDF take control of the
// process. What the attacker then gets is what this wrapper leaves: with bubblewrap only the
// working folder, no network, no other processes, no home folder, no database; without it, at
// least hard limits on memory, CPU time and output size.
//
// Bwrap is the path of the bwrap binary; empty means "limits only".
type Sandbox struct {
	Bwrap string
}

// Limits for one converter run. The address-space cap is generous because Ghostscript maps more
// than it touches; the real memory is still bounded by the Pi, and the process is killed when it
// goes past the cap instead of taking the database down with it.
const (
	sandboxMemBytes  = 1536 << 20
	sandboxCPUSecs   = 100
	sandboxFileBytes = 64 << 20
	sandboxMaxFiles  = 64
	sandboxKillGrace = 2 * time.Second
)

// bwrapWorks runs a trivial program inside bubblewrap. It fails where user namespaces are
// disabled (some hardened kernels and containers), and the caller then falls back to limits only.
func bwrapWorks(bwrap string) bool {
	s := Sandbox{Bwrap: bwrap}
	return s.Command(context.Background(), os.TempDir(), "/usr/bin/env", "true").Run() == nil
}

// Command builds the command to run name with args inside the sandbox, with workDir as the only
// writable folder. Cancelling ctx kills the whole process group, not just the first process.
func (s Sandbox) Command(ctx context.Context, workDir, name string, args ...string) *exec.Cmd {
	inner := []string{name}
	inner = append(inner, args...)
	if p, err := exec.LookPath("prlimit"); err == nil {
		inner = append([]string{p,
			"--as=" + strconv.Itoa(sandboxMemBytes),
			"--cpu=" + strconv.Itoa(sandboxCPUSecs),
			"--fsize=" + strconv.Itoa(sandboxFileBytes),
			"--nofile=" + strconv.Itoa(sandboxMaxFiles),
			"--core=0",
			"--"}, inner...)
	}

	var cmd *exec.Cmd
	if s.Bwrap != "" {
		// bwrap --clearenv sets the environment inside, so the outer one stays empty.
		cmd = exec.CommandContext(ctx, s.Bwrap, append(s.bwrapArgs(workDir), inner...)...)
		cmd.Env = []string{}
	} else {
		cmd = exec.CommandContext(ctx, inner[0], inner[1:]...)
		cmd.Dir = workDir
		cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + workDir, "TMPDIR=" + workDir, "LC_ALL=C"}
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = sandboxKillGrace
	return cmd
}

func (s Sandbox) bwrapArgs(workDir string) []string {
	a := []string{
		"--unshare-all", // new user, mount, pid, ipc, uts and network namespaces: no network at all
		"--die-with-parent", "--new-session",
		"--ro-bind", "/usr", "/usr",
	}
	// /bin, /lib... are symlinks into /usr on current distributions; recreate whichever form this
	// machine uses so the dynamic loader is found.
	for _, p := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
		if target, err := os.Readlink(p); err == nil {
			a = append(a, "--symlink", target, p)
		} else if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			a = append(a, "--ro-bind", p, p)
		}
	}
	a = append(a,
		"--ro-bind-try", "/etc/fonts", "/etc/fonts",
		"--ro-bind-try", "/etc/ghostscript", "/etc/ghostscript",
		"--proc", "/proc", "--dev", "/dev",
		"--tmpfs", "/tmp", // before the bind below: the working folder usually lives under /tmp
		"--bind", workDir, workDir,
		"--chdir", workDir,
		"--clearenv",
		"--setenv", "PATH", "/usr/bin:/bin",
		"--setenv", "HOME", workDir,
		"--setenv", "TMPDIR", workDir,
		"--setenv", "LC_ALL", "C",
		"--",
	)
	return a
}
