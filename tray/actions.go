package tray

import (
	"context"
	"io"
	"os"
	"os/exec"
	"os/signal"

	"github.com/NordSecurity/nordvpn-linux/cli"
	nordclient "github.com/NordSecurity/nordvpn-linux/client"
	"github.com/NordSecurity/nordvpn-linux/daemon/pb"
	"github.com/NordSecurity/nordvpn-linux/internal"
	meshpb "github.com/NordSecurity/nordvpn-linux/meshnet/pb"
)

// The pattern for actions is to return 'true' on success and 'false' (along with emitting a notification) on failure

func (ti *Instance) login() {
	resp, err := ti.Client.IsLoggedIn(context.Background(), &pb.Empty{})
	if err != nil || resp.GetValue() {
		ti.notify(pWarning, "You are already logged in")
		return
	}

	cl, err := ti.Client.LoginOAuth2(
		context.Background(),
		&pb.Empty{},
	)
	if err != nil {
		ti.notify(pError, "Login error: %s", err)
		return
	}

	for {
		resp, err := cl.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			ti.notify(pError, "Login error: %s", err)
			return
		}

		if url := resp.GetData(); url != "" {
			// #nosec G204 -- user input is not passed in
			cmd := exec.Command("xdg-open", url)
			err = cmd.Start()
			if err != nil {
				ti.notify(pWarning, "Failed to start xdg-open: %v", err)
			}
			err = cmd.Wait()

			if err != nil {
				ti.notify(pWarning, "Failed to open the web browser: %v", err)
				ti.notify(pInfo, "Continue log in in the browser: %s", url)
			}
		}
	}
}

func (ti *Instance) logout(persistToken bool) bool {
	payload, err := ti.Client.Logout(context.Background(), &pb.LogoutRequest{
		PersistToken: persistToken,
	})
	if err != nil {
		ti.notify(pError, "Logout error: %s", err)
		return false
	}

	switch payload.Type {
	case internal.CodeSuccess:
		if !ti.NotifyEnabled {
			ti.notify(pInfo, cli.LogoutSuccess)
		}
		return true
	case internal.CodeTokenInvalidated:
		if !ti.NotifyEnabled {
			ti.notify(pInfo, cli.LogoutTokenSuccess)
		}
		return true
	default:
		ti.notify(pError, cli.CheckYourInternetConnMessage)
		return false
	}
}

func (ti *Instance) connect(serverTag string, serverGroup string) bool {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, os.Interrupt)
	defer close(ch)
	go func(ch chan os.Signal) {
		for range ch {
			// #nosec G104 -- LVPN-2090
			ti.Client.Disconnect(context.Background(), &pb.Empty{})
		}
	}(ch)

	resp, err := ti.Client.Connect(context.Background(), &pb.ConnectRequest{
		ServerTag:   serverTag,
		ServerGroup: serverGroup,
	})
	if err != nil {
		ti.notify(pError, "Connect error: %s", err)
		return false
	}

	for {
		out, err := resp.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			ti.notify(pError, "Connect error: %s", err)
			return false
		}

		switch out.Type {
		case internal.CodeFailure:
			ti.notify(pError, "Connect error: %s", nordclient.ConnectCantConnect)
		case internal.CodeExpiredRenewToken:
			ti.notify(pWarning, nordclient.RelogRequest)
			ti.login()
			return ti.connect(serverTag, serverGroup)
		case internal.CodeTokenRenewError:
			ti.notify(pError, nordclient.AccountTokenRenewError)
		case internal.CodeAccountExpired:
			ti.notify(pError, cli.ErrAccountExpired.Error())
		case internal.CodeDisconnected:
			ti.notify(pInfo, internal.DisconnectSuccess)
		case internal.CodeTagNonexisting:
			ti.notify(pError, internal.TagNonexistentErrorMessage)
		case internal.CodeGroupNonexisting:
			ti.notify(pError, internal.GroupNonexistentErrorMessage)
		case internal.CodeServerUnavailable:
			ti.notify(pError, internal.ServerUnavailableErrorMessage)
		case internal.CodeDoubleGroupError:
			ti.notify(pError, internal.DoubleGroupErrorMessage)
		case internal.CodeVPNRunning:
			ti.notify(pWarning, nordclient.ConnectConnected)
		case internal.CodeUFWDisabled:
			ti.notify(pWarning, nordclient.UFWDisabledMessage)
		case internal.CodeConnecting:
		case internal.CodeConnected:
			return true
		}
	}

	return false
}

func (ti *Instance) disconnect() bool {
	resp, err := ti.Client.Disconnect(context.Background(), &pb.Empty{})
	if err != nil {
		ti.notify(pError, "Disconnect error: %s", err)
		return false
	}

	for {
		out, err := resp.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			ti.notify(pError, "Disconnect error: %s", err)
			return false
		}

		switch out.Type {
		case internal.CodeVPNNotRunning:
			ti.notify(pWarning, cli.DisconnectNotConnected)
		case internal.CodeDisconnected:
			if !ti.NotifyEnabled {
				ti.notify(pInfo, internal.DisconnectSuccess)
			}
		}
	}
	return true
}

// nolint:unused
func (ti *Instance) enableMeshnet() bool {
	resp, err := ti.MeshClient.EnableMeshnet(context.Background(), &meshpb.Empty{})
	if err != nil {
		ti.notify(pError, "Enable meshnet error: %s", err)
		return false
	}
	if err := cli.MeshnetResponseToError(resp); err != nil {
		ti.notify(pError, "Enable meshnet error: %s", err)
		return false
	}

	if !ti.NotifyEnabled {
		ti.notify(pInfo, cli.MsgSetMeshnetSuccess, "enabled")
	}

	// TODO: c.fileshareProcessManager.StartProcess() is called here in the CLI
	return true
}

// nolint:unused
func (ti *Instance) disableMeshnet() bool {
	resp, err := ti.MeshClient.DisableMeshnet(context.Background(), &meshpb.Empty{})
	if err != nil {
		ti.notify(pError, "Disable meshnet error: %s", err)
		return false
	}
	if err := cli.MeshnetResponseToError(resp); err != nil {
		ti.notify(pError, "Disable meshnet error: %s", err)
		return false
	}

	if !ti.NotifyEnabled {
		ti.notify(pInfo, cli.MsgSetMeshnetSuccess, "disabled")
	}

	return true
}
