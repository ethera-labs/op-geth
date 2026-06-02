package permissions

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
)

// snapshotJSON mirrors the live GET /api/v1/config/snapshot payload: entities
// and rule groups are separate collections joined by ruleGroupId, and an entity
// carries multiple wallet addresses.
const snapshotJSON = `{
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
    }
  ],
  "ruleGroups": [
    {
      "id": 10,
      "canSendTx": true,
      "canDeployContract": false,
      "networkScope": "restricted",
      "networkRollups": [222, 333]
    },
    {
      "id": 20,
      "canSendTx": true,
      "canDeployContract": true,
      "networkScope": "all",
      "networkRollups": []
    }
  ]
}`

func TestResolveRules(t *testing.T) {
	var snapshot snapshotResponse
	require.NoError(t, json.Unmarshal([]byte(snapshotJSON), &snapshot))

	rules := resolveRules(snapshot)

	bankHot := common.HexToAddress("0x000000000000000000000000000000000000aaaa")
	bankCold := common.HexToAddress("0x000000000000000000000000000000000000bbbb")
	open := common.HexToAddress("0x000000000000000000000000000000000000cccc")
	ungrouped := common.HexToAddress("0x000000000000000000000000000000000000dddd")

	// Every wallet of an entity inherits its rule group.
	for _, addr := range []common.Address{bankHot, bankCold} {
		r, known := rules[addr]
		require.True(t, known)
		require.False(t, r.CanDeployContract)
		require.True(t, r.NetworkRestricted)
		require.True(t, r.ChainAllowed(222))
		require.True(t, r.ChainAllowed(333))
		require.False(t, r.ChainAllowed(444))
	}

	r, known := rules[open]
	require.True(t, known)
	require.True(t, r.CanDeployContract)
	require.False(t, r.NetworkRestricted)
	require.True(t, r.ChainAllowed(999)) // unrestricted reaches any peer

	// An entity with no rule group is not managed.
	_, known = rules[ungrouped]
	require.False(t, known)
}
