package interop

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types/interoptypes"
)

func TestVerificationClientVerifyTx(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodPost, r.Method)
			require.Equal(t, "application/json", r.Header.Get("Content-Type"))

			var payload VerificationRequest
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			require.Equal(t, common.HexToHash("0x01"), payload.TxHash)
			require.Equal(t, hexutil.Bytes{0xaa, 0xbb}, payload.TxData)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("I confirm"))
		}))
		defer server.Close()

		client := NewVerificationClient(server.URL, time.Second)
		err := client.VerifyTx(context.Background(), VerificationRequest{
			TxHash:       common.HexToHash("0x01"),
			TxData:       hexutil.Bytes{0xaa, 0xbb},
			InboxEntries: []common.Hash{{0xaa}},
			MinSafety:    interoptypes.CrossUnsafe,
		})
		require.NoError(t, err)
	})

	t.Run("reject", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("reject"))
		}))
		defer server.Close()

		client := NewVerificationClient(server.URL, time.Second)
		err := client.VerifyTx(context.Background(), VerificationRequest{})
		require.ErrorContains(t, err, "verification rejected with status 403: reject")
	})
}
