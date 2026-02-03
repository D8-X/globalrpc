package globalrpc

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
)

type RpcConfig struct {
	ChainId int      `json:"chainId"`
	Wss     []string `json:"wss"`
	Https   []string `json:"https"`
}

type RPCKind int

const (
	TypeHTTPS RPCKind = iota // Starts at 0
	TypeWSS                  // 1
)

func (t RPCKind) String() string {
	switch t {
	case TypeHTTPS:
		return "HTTPS"
	case TypeWSS:
		return "WSS"
	default:
		return "Unknown"
	}
}

// NonRetryableError wraps an error to signal that RpcQuery should not retry.
// Use NewNonRetryableError to wrap errors that indicate a previous attempt
// already had a side effect (e.g. a transaction was submitted).
type NonRetryableError struct {
	Err error
}

func (e *NonRetryableError) Error() string {
	return e.Err.Error()
}

func (e *NonRetryableError) Unwrap() error {
	return e.Err
}

// NewNonRetryableError wraps err so that RpcQuery will not retry.
func NewNonRetryableError(err error) error {
	return &NonRetryableError{Err: err}
}

// IsNonRetryable reports whether err (or any error in its chain) is a
// NonRetryableError.
func IsNonRetryable(err error) bool {
	var nre *NonRetryableError
	return errors.As(err, &nre)
}

func loadRPCConfig(chainId int, filename string) (RpcConfig, error) {
	var config []RpcConfig
	jsonFile, err := os.Open(filename)
	if err != nil {
		return RpcConfig{}, err
	}
	defer jsonFile.Close()

	// Read the file's contents into a byte slice
	byteValue, err := io.ReadAll(jsonFile)
	if err != nil {
		log.Fatalf("Failed to read file: %v", err)
	}
	// Unmarshal the JSON data
	err = json.Unmarshal(byteValue, &config)
	if err != nil {
		return RpcConfig{}, fmt.Errorf("unmarshalling JSON failed: %v", err)
	}
	idx := -1
	for j := range config {
		if config[j].ChainId == chainId {
			idx = j
			break
		}
	}
	if idx == -1 {
		return RpcConfig{}, fmt.Errorf("no rpc config for chain %d", chainId)
	}

	return config[idx], nil
}
