package daemon

import (
	"context"
	"testing"

	"github.com/NordSecurity/nordvpn-linux/config"
	"github.com/NordSecurity/nordvpn-linux/daemon/pb"
	"github.com/NordSecurity/nordvpn-linux/events/subs"
	"github.com/NordSecurity/nordvpn-linux/fileshare/service"
	"github.com/NordSecurity/nordvpn-linux/internal"
	"github.com/NordSecurity/nordvpn-linux/test/category"
	"github.com/NordSecurity/nordvpn-linux/test/mock/networker"
	mapset "github.com/deckarep/golang-set"
	"github.com/stretchr/testify/assert"
)

func configureRPC(dm *DataManager, cm config.Manager) *RPC {
	return &RPC{
		ac:        &workingLoginChecker{},
		cm:        cm,
		dm:        dm,
		fileshare: service.NoopFileshare{},
		netw:      &networker.Mock{},
		ncClient:  mockNC{},
		publisher: &subs.Subject[string]{},
		api:       mockApi{},
	}
}

func setupTest(t *testing.T) {
	category.Set(t, category.Unit)
}

func tearDown(t *testing.T) {
	if r := recover(); r != nil {
		t.Error("The app crashed")
	}

	testsCleanup()
}

func TestRPCGroups(t *testing.T) {
	setupTest(t)
	defer tearDown(t)

	tests := []struct {
		name       string
		dm         *DataManager
		cm         config.Manager
		statusCode int64
	}{
		{
			name:       "DataManager and config.Manager are nil",
			dm:         nil,
			cm:         nil,
			statusCode: internal.CodeEmptyPayloadError,
		},
		{
			name:       "DataManager is nil",
			dm:         nil,
			cm:         newMockConfigManager(),
			statusCode: internal.CodeEmptyPayloadError,
		},
		{
			name:       "missing configuration file",
			dm:         testNewDataManager(),
			cm:         failingConfigManager{},
			statusCode: internal.CodeConfigError,
		},
		{
			name:       "app data is empty",
			dm:         testNewDataManager(),
			cm:         newMockConfigManager(),
			statusCode: internal.CodeEmptyPayloadError,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rpc := configureRPC(test.dm, test.cm)
			payload, _ := rpc.Groups(context.Background(), &pb.GroupsRequest{})

			assert.Equal(t, test.statusCode, payload.Type)
		})
	}
}

func TestRPCGroups_Successful(t *testing.T) {
	setupTest(t)
	defer tearDown(t)

	dm := testNewDataManager()

	groupNames := map[bool]map[config.Protocol]mapset.Set{
		false: {
			config.Protocol_UDP: mapset.NewSet("false_Protocol_UDP"),
			config.Protocol_TCP: mapset.NewSet("false_Protocol_TCP"),
		},
	}
	dm.SetAppData(nil, nil, groupNames)

	cm := newMockConfigManager()
	cm.c.AutoConnectData.Protocol = config.Protocol_TCP

	rpc := configureRPC(dm, cm)

	groupsRequest := &pb.GroupsRequest{}
	groupsRequest.Obfuscate = false

	payload, _ := rpc.Groups(context.Background(), groupsRequest)
	assert.Equal(t, internal.CodeSuccess, payload.GetType())
	assert.Equal(t, []string{"false_Protocol_TCP"}, payload.GetData())
}