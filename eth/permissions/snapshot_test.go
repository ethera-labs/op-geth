package permissions

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
)

// streamJSON mirrors a config-stream message: a "snapshot" envelope whose data
// carries entities and rule groups as separate collections joined by
// ruleGroupId, with each entity holding multiple wallet addresses.
const streamJSON = `{
  "type": "snapshot",
  "data": {
    "version": 2,
    "entities": [
      {
        "id": 1,
        "name": "Bank",
        "isActive": true,
        "ruleGroupId": 10,
        "walletAddresses": [
          {"address": "0x000000000000000000000000000000000000aaaa", "label": "hot"},
          {"address": "0x000000000000000000000000000000000000bbbb", "label": "cold"}
        ]
      },
      {
        "id": 2,
        "name": "Open",
        "isActive": true,
        "ruleGroupId": 20,
        "walletAddresses": [{"address": "0x000000000000000000000000000000000000cccc"}]
      },
      {
        "id": 3,
        "name": "No group",
        "isActive": true,
        "walletAddresses": [{"address": "0x000000000000000000000000000000000000dddd"}]
      },
      {
        "id": 4,
        "name": "Disabled",
        "isActive": false,
        "ruleGroupId": 20,
        "walletAddresses": [{"address": "0x000000000000000000000000000000000000eeee"}]
      }
    ],
    "ruleGroups": [
      {
        "id": 10,
        "canDeployContract": false,
        "networkScope": "restricted",
        "rollups": [222, 333]
      },
      {
        "id": 20,
        "canDeployContract": true,
        "networkScope": "all",
        "rollups": []
      }
    ]
  }
}`

func TestResolveRules(t *testing.T) {
	var msg streamMessage
	require.NoError(t, json.Unmarshal([]byte(streamJSON), &msg))
	require.Equal(t, "snapshot", msg.Type)

	rules := resolveRules(msg.Data)

	bankHot := common.HexToAddress("0x000000000000000000000000000000000000aaaa")
	bankCold := common.HexToAddress("0x000000000000000000000000000000000000bbbb")
	open := common.HexToAddress("0x000000000000000000000000000000000000cccc")
	ungrouped := common.HexToAddress("0x000000000000000000000000000000000000dddd")
	disabled := common.HexToAddress("0x000000000000000000000000000000000000eeee")

	// Every wallet of an entity inherits its rule group.
	for _, addr := range []common.Address{bankHot, bankCold} {
		r, known := rules[addr]
		require.True(t, known)
		require.True(t, r.Active)
		require.False(t, r.CanDeployContract)
		require.True(t, r.NetworkRestricted)
		require.True(t, r.ChainAllowed(222))
		require.True(t, r.ChainAllowed(333))
		require.False(t, r.ChainAllowed(444))
	}

	r, known := rules[open]
	require.True(t, known)
	require.True(t, r.Active)
	require.True(t, r.CanDeployContract)
	require.False(t, r.NetworkRestricted)
	require.True(t, r.ChainAllowed(999)) // unrestricted reaches any peer

	// An active entity with no rule group is not managed.
	_, known = rules[ungrouped]
	require.False(t, known)

	// A disabled entity is managed but blocked outright.
	r, known = rules[disabled]
	require.True(t, known)
	require.False(t, r.Active)
}
