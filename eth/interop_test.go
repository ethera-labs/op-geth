package eth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/eth/ethconfig"
)

func TestValidateInteropVerificationConfig(t *testing.T) {
	base := ethconfig.Defaults
	base.InteropMessageRPC = "http://supervisor"
	base.InteropMempoolFiltering = true
	base.InteropVerificationEnabled = true
	base.InteropVerificationURL = "http://verifier"
	base.InteropVerificationTimeout = time.Second

	t.Run("valid", func(t *testing.T) {
		cfg := base
		require.NoError(t, validateInteropVerificationConfig(&cfg))
	})

	t.Run("missing url", func(t *testing.T) {
		cfg := base
		cfg.InteropVerificationURL = ""
		require.ErrorContains(t, validateInteropVerificationConfig(&cfg), "rollup.interopverificationurl")
	})

	t.Run("missing interop rpc", func(t *testing.T) {
		cfg := base
		cfg.InteropMessageRPC = ""
		require.ErrorContains(t, validateInteropVerificationConfig(&cfg), "rollup.interoprpc")
	})

	t.Run("disabled mempool filtering", func(t *testing.T) {
		cfg := base
		cfg.InteropMempoolFiltering = false
		require.ErrorContains(t, validateInteropVerificationConfig(&cfg), "rollup.interopmempoolfiltering")
	})
}
