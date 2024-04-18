package service

import (
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"

	"github.com/NordSecurity/nordvpn-linux/internal"
)

// ErrNotStarted when disabling norduser
var ErrNotStarted = errors.New("norduserd wasn't started")

// ChildProcessNorduser manages norduser service through exec.Command
type ChildProcessNorduser struct {
	mu sync.Mutex
}

func NewChildProcessNorduser() *ChildProcessNorduser {
	return &ChildProcessNorduser{}
}

// handlePsError returns nil if err is nil or error code 1(no processes listed). It returns unmodified err in any other
// case.
func handlePsError(err error) error {
	if err == nil {
		return nil
	}

	var exiterr *exec.ExitError
	if errors.As(err, &exiterr) {
		// ps returns 1 when no processes are shown
		if exiterr.ExitCode() == 1 {
			return nil
		}
	}

	return err
}

func parseNorduserPIDs(psOutput string) []int {
	pids := []int{}
	for _, pidStr := range strings.Split(psOutput, "\n") {
		pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
		if err != nil {
			log.Println("failed to parse pid string: ", err)
			continue
		}

		pids = append(pids, pid)
	}

	return pids
}

func getRunningNorduserPIDs() ([]int, error) {
	// #nosec G204 -- arguments are constant
	output, err := exec.Command("ps", "-C", internal.Norduserd, "-o", "pid=").CombinedOutput()
	if err := handlePsError(err); err != nil {
		return []int{}, fmt.Errorf("listing norduser pids: %w", err)
	}

	return parseNorduserPIDs(string(output)), nil
}

func findPIDOfUID(uids string, uid uint32) int {
	desiredUID := fmt.Sprint(uid)
	for _, pidUid := range strings.Split(uids, "\n") {
		pidUidSplit := strings.Split(strings.TrimSpace(pidUid), " ")
		if len(pidUidSplit) != 2 {
			log.Println("unexpected ps output: ", pidUid)
		}

		uid := pidUidSplit[0]
		if uid != desiredUID {
			continue
		}

		pid := pidUidSplit[1]
		pidInt, err := strconv.Atoi(pid)
		if err != nil {
			log.Println("failed to parse pid: ", err)
			continue
		}

		return pidInt
	}

	return -1
}

func getPIDForNorduserUID(uid uint32) (int, error) {
	// list all norduserd processes, restrict output to uid of the owner
	// #nosec G204 -- arguments are constant
	output, err := exec.Command("ps", "-C", internal.Norduserd, "-o", "uid=", "-o", "pid=").CombinedOutput()
	if err := handlePsError(err); err != nil {
		return -1, fmt.Errorf("listing norduser uids/pids: %w", err)
	}

	return findPIDOfUID(string(output), uid), nil
}

func isUIDPresent(uids string, uid uint32) bool {
	desiredUID := fmt.Sprint(uid)
	for _, uid := range strings.Split(uids, "\n") {
		if strings.Trim(uid, " ") == desiredUID {
			return true
		}
	}

	return false
}

func isRunning(uid uint32) (bool, error) {
	// list all norduserd processes, restrict output to uid of the owner
	// #nosec G204 -- arguments are constant
	output, err := exec.Command("ps", "-C", internal.Norduserd, "-o", "uid=").CombinedOutput()
	if err := handlePsError(err); err != nil {
		return false, fmt.Errorf("listing norduser uids: %w", err)
	}

	return isUIDPresent(string(output), uid), nil
}

// Enable starts norduser process
func (c *ChildProcessNorduser) Enable(uid uint32, gid uint32, home string) (err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	running, err := isRunning(uid)
	if err != nil {
		return fmt.Errorf("failed to determine if the process is already running: %w", err)
	}

	if running {
		return nil
	}

	nordvpnGid, err := internal.GetNordvpnGid()
	if err != nil {
		return fmt.Errorf("determining nordvpn gid: %w", err)
	}

	// #nosec G204 -- no input comes from user
	cmd := exec.Command("/usr/bin/" + internal.Norduserd)
	credential := &syscall.Credential{
		Uid:    uid,
		Gid:    gid,
		Groups: []uint32{uint32(nordvpnGid)},
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
	// os.UserHomeDir always returns value of $HOME and spawning child process copies
	// environment variables from a parent process, therefore value of $HOME will be root home
	// dir, where user usually does not have access.
	cmd.Env = append(cmd.Env, "HOME="+home)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the process: %w", err)
	}

	go cmd.Wait()

	return nil
}

// Stop teminates norduser process
func (c *ChildProcessNorduser) Stop(uid uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	pid, err := getPIDForNorduserUID(uid)
	if err != nil {
		return fmt.Errorf("looking up norduser pid: %w", err)
	}

	if pid == -1 {
		return nil
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		if errno, ok := err.(syscall.Errno); ok {
			if errno == syscall.ESRCH {
				return nil
			}
		}
		return fmt.Errorf("sending SIGTERM to norduser process: %w", err)
	}

	return nil
}

func (c *ChildProcessNorduser) StopAll() {
	c.mu.Lock()
	defer c.mu.Unlock()

	pids, err := getRunningNorduserPIDs()
	if err != nil {
		return
	}

	for _, pid := range pids {
		if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
			log.Println("failed to send a signal to norduser process: ", err)
		}
	}
}