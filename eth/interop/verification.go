package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types/interoptypes"
)

type VerificationRequest struct {
	TxHash              common.Hash                      `json:"txHash"`
	TxData              hexutil.Bytes                    `json:"txData"`
	InboxEntries        []common.Hash                    `json:"inboxEntries"`
	MinSafety           interoptypes.SafetyLevel         `json:"minSafety"`
	ExecutingDescriptor interoptypes.ExecutingDescriptor `json:"executingDescriptor"`
}

type VerificationClient struct {
	client   *http.Client
	endpoint string
}

func NewVerificationClient(endpoint string, timeout time.Duration) *VerificationClient {
	return &VerificationClient{
		client: &http.Client{
			Timeout: timeout,
		},
		endpoint: endpoint,
	}
}

func (cl *VerificationClient) Close() {
	if cl == nil || cl.client == nil {
		return
	}
	cl.client.CloseIdleConnections()
}

func (cl *VerificationClient) VerifyTx(ctx context.Context, payload VerificationRequest) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal verification payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cl.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build verification request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := cl.client.Do(req)
	if err != nil {
		return fmt.Errorf("send verification request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	respBody, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if readErr != nil {
		return fmt.Errorf("verification rejected with status %d", resp.StatusCode)
	}

	message := strings.TrimSpace(string(respBody))
	if message == "" {
		return fmt.Errorf("verification rejected with status %d", resp.StatusCode)
	}
	return fmt.Errorf("verification rejected with status %d: %s", resp.StatusCode, message)
}
