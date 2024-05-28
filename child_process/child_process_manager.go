// Package child_process contains common utilities for running NordVPN helper apps(eg. fileshare and norduser) as a
// child process, rather than a system daemon.
package childprocess

type StartupErrorCode int

const (
	CodeAlreadyRunning StartupErrorCode = iota + 1
	CodeAlreadyRunningForOtherUser
	CodeFailedToCreateUnixScoket
	CodeMeshnetNotEnabled
	CodeAddressAlreadyInUse
	CodeFailedToEnable
	CodeUserNotInGroup
)

type ProcessStatus int

const (
	Running ProcessStatus = iota
	RunningForOtherUser
	NotRunning
)

type ChildProcessManager interface {
	// StartProcess starts the process
	StartProcess() (StartupErrorCode, error)
	// StopProcess stops the process
	StopProcess(disable bool) error
	// RestartProcess restarts the process
	RestartProcess() error
	// ProcessStatus checks the status of process
	ProcessStatus() ProcessStatus
}
