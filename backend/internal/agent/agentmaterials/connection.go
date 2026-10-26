package agentmaterials

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
)

// ConnectionContractVersion is the agent connection file format the running
// sidecar publishes in its Data Home (ADR SEC-250).
const ConnectionContractVersion = 1

const maxConnectionFileBytes = 4096

// NewFromConnection reads the owner-only connection file the running sidecar
// published and returns a client that authenticates with its scoped agent
// token. An explicit server, when given, must match the file exactly; the
// token never comes from an HTTP response.
func NewFromConnection(path, server string) (*Client, *Error) {
	if path == "" {
		return nil, problem("AGENT_CONNECTION_INVALID")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, problem("AGENT_CONNECTION_UNAVAILABLE")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, maxConnectionFileBytes+1))
	if err != nil || len(raw) > maxConnectionFileBytes {
		return nil, problem("AGENT_CONNECTION_INVALID")
	}
	var connection struct {
		ContractVersion int    `json:"contract_version"`
		Server          string `json:"server"`
		Token           string `json:"token"`
	}
	if json.Unmarshal(raw, &connection) != nil || connection.ContractVersion != ConnectionContractVersion {
		return nil, problem("AGENT_CONNECTION_INVALID")
	}
	if server != "" && server != connection.Server {
		return nil, problem("AGENT_CONNECTION_INVALID")
	}
	if !validToken(connection.Token) {
		return nil, problem("AGENT_CONNECTION_INVALID")
	}
	client, e := New(connection.Server)
	if e != nil {
		return nil, e
	}
	client.token = connection.Token
	return client, nil
}

func validToken(token string) bool {
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	return err == nil && len(decoded) == 32 && base64.RawURLEncoding.EncodeToString(decoded) == token
}
