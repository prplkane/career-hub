package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"careerhub.local/backend/internal/runtimepaths"
)

// The agent connection file (ADR SEC-250) tells career-hub-agent where the
// running sidecar listens and which scoped token to use. It lives in the Data
// Home, whose protected DACL admits only the owner and SYSTEM, under a
// directory that backups never include. One process owns the Home, so the
// file is replaced at every start and withdrawn at shutdown. A crash can leave
// a file whose token belongs to a dead process; the next start replaces it.
const (
	agentConnectionDirectory = "session"
	agentConnectionPath      = "session/agent-connection.json"
	agentConnectionContract  = 1
	agentConnectionMaxBytes  = 4096
	agentConnectionTimeout   = 5 * time.Second
)

var errAgentConnection = errors.New("agent connection file could not be published")

type agentConnection struct {
	ContractVersion int    `json:"contract_version"`
	Server          string `json:"server"`
	Token           string `json:"token"`
}

type agentSession struct {
	dataRoot string

	mu        sync.Mutex
	published bool
	size      int64
	digest    string
}

func (s *agentSession) publish(port int, token string) error {
	if port < 1 || port > 65535 || token == "" {
		return errAgentConnection
	}
	content, err := json.Marshal(agentConnection{
		ContractVersion: agentConnectionContract,
		Server:          "http://127.0.0.1:" + strconv.Itoa(port),
		Token:           token,
	})
	if err != nil {
		return errAgentConnection
	}
	root, problem := runtimepaths.Open(s.dataRoot)
	if problem != nil {
		return errAgentConnection
	}
	defer root.Close()
	if _, problem := root.EnsureDirectory(agentConnectionDirectory); problem != nil {
		return errAgentConnection
	}
	if err := removeExactFile(root, agentConnectionPath); err != nil {
		return err
	}
	if created, problem := root.CreateFile(agentConnectionPath, content); problem != nil || !created {
		return errAgentConnection
	}
	sum := sha256.Sum256(content)
	s.mu.Lock()
	s.published, s.size, s.digest = true, int64(len(content)), hex.EncodeToString(sum[:])
	s.mu.Unlock()
	return nil
}

// release withdraws only the exact file this process published. A file that
// changed since publication is left alone rather than deleted blindly.
func (s *agentSession) release() {
	s.mu.Lock()
	published, size, digest := s.published, s.size, s.digest
	s.published = false
	s.mu.Unlock()
	if !published {
		return
	}
	root, problem := runtimepaths.Open(s.dataRoot)
	if problem != nil {
		return
	}
	defer root.Close()
	ctx, cancel := context.WithTimeout(context.Background(), agentConnectionTimeout)
	defer cancel()
	_, _ = root.RemoveBackupFile(ctx, agentConnectionPath, size, digest)
}

// removeExactFile deletes a previous connection file, if any, after reading it
// once so the guarded removal can verify it is the same single regular file.
func removeExactFile(root *runtimepaths.Root, relative string) error {
	previous, found, problem := root.ReadSingleLinkFile(relative, agentConnectionMaxBytes)
	if problem != nil {
		return errAgentConnection
	}
	if !found {
		return nil
	}
	sum := sha256.Sum256(previous)
	ctx, cancel := context.WithTimeout(context.Background(), agentConnectionTimeout)
	defer cancel()
	outcome, problem := root.RemoveBackupFile(ctx, relative, int64(len(previous)), hex.EncodeToString(sum[:]))
	if problem != nil || outcome != runtimepaths.BackupRemoved {
		return errAgentConnection
	}
	return nil
}
