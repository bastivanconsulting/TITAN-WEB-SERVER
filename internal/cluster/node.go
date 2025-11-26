package cluster

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/hashicorp/raft"
	raftboltdb "github.com/hashicorp/raft-boltdb"
	"titan-web-server/internal/config"
	"titan-web-server/internal/config/parser"
)

type ConfigUpdateCallback func(*config.Config) error

type FSM struct {
	mu           sync.Mutex
	config       *config.Config
	callback     ConfigUpdateCallback
	configPath   string
}

func NewFSM(initialConfig *config.Config, path string, cb ConfigUpdateCallback) *FSM {
	return &FSM{
		config:     initialConfig,
		callback:   cb,
		configPath: path,
	}
}

// LogEntry represents the data stored in the Raft log
type LogEntry struct {
	Type    string // "config_update"
	Payload []byte // The raw config file content
}

// Apply applies a Raft log entry to the FSM
func (f *FSM) Apply(l *raft.Log) interface{} {
	var entry LogEntry
	if err := json.Unmarshal(l.Data, &entry); err != nil {
		log.Printf("failed to unmarshal log entry: %v", err)
		return err
	}

	if entry.Type == "config_update" {
		f.mu.Lock()
		defer f.mu.Unlock()

		// Parse the new config
		p := parser.NewParser(string(entry.Payload))
		ast, err := p.Parse()
		if err != nil {
			log.Printf("failed to parse config from raft: %v", err)
			return err
		}

		newConfig, err := config.DecodeAST(ast)
		if err != nil {
			log.Printf("failed to decode config from raft: %v", err)
			return err
		}

		// Validate
		if err := newConfig.Validate(); err != nil {
			log.Printf("invalid config from raft: %v", err)
			return err
		}

		// Update internal state
		f.config = newConfig

		// Trigger callback to reload server
		// The server will handle writing the config to disk
		if f.callback != nil {
			go func() {
				if err := f.callback(newConfig); err != nil {
					log.Printf("failed to reload server: %v", err)
				}
			}()
		}
		return nil
	}

	return nil
}

// Snapshot returns a snapshot of the current state
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Serialize the in-memory config to ensure we snapshot the actual state
	// independent of the file system.
	content := config.EncodeToConf(f.config)

	return &ConfigSnapshot{configContent: []byte(content)}, nil
}

// Restore restores the FSM from a snapshot
func (f *FSM) Restore(rc io.ReadCloser) error {
	content, err := io.ReadAll(rc)
	if err != nil {
		return err
	}

	p := parser.NewParser(string(content))
	ast, err := p.Parse()
	if err != nil {
		return err
	}

	cfg, err := config.DecodeAST(ast)
	if err != nil {
		return err
	}

	f.mu.Lock()
	f.config = cfg
	f.mu.Unlock()

	// Write to disk
	return os.WriteFile(f.configPath, content, 0644)
}

type ConfigSnapshot struct {
	configContent []byte
}

func (s *ConfigSnapshot) Persist(sink raft.SnapshotSink) error {
	err := func() error {
		if _, err := sink.Write(s.configContent); err != nil {
			return err
		}
		return nil
	}()
	if err != nil {
		sink.Cancel()
		return err
	}
	return sink.Close()
}

func (s *ConfigSnapshot) Release() {}

// Node represents a node in the Raft cluster
type Node struct {
	Raft *raft.Raft
	FSM  *FSM
	ID   string
	Dir  string
}

func NewNode(id string, bindAddr, advertiseAddr string, dataDir string, fsm *FSM, bootstrap bool) (*Node, error) {
	config := raft.DefaultConfig()
	config.LocalID = raft.ServerID(id)

	advertise, err := net.ResolveTCPAddr("tcp", advertiseAddr)
	if err != nil {
		return nil, err
	}

	transport, err := raft.NewTCPTransport(bindAddr, advertise, 3, 10*time.Second, os.Stderr)
	if err != nil {
		return nil, err
	}

	snapshots, err := raft.NewFileSnapshotStore(dataDir, 2, os.Stderr)
	if err != nil {
		return nil, err
	}

	boltDB, err := raftboltdb.NewBoltStore(filepath.Join(dataDir, "raft.db"))
	if err != nil {
		return nil, err
	}

	logStore := boltDB
	stableStore := boltDB

	ra, err := raft.NewRaft(config, fsm, logStore, stableStore, snapshots, transport)
	if err != nil {
		return nil, err
	}

	if bootstrap {
		configuration := raft.Configuration{
			Servers: []raft.Server{
				{
					ID:      config.LocalID,
					Address: transport.LocalAddr(),
				},
			},
		}
		ra.BootstrapCluster(configuration)
	}

	return &Node{
		Raft: ra,
		FSM:  fsm,
		ID:   id,
		Dir:  dataDir,
	}, nil
}

// ApplyConfigUpdate submits a config update to the cluster
func (n *Node) ApplyConfigUpdate(configContent []byte) error {
	entry := LogEntry{
		Type:    "config_update",
		Payload: configContent,
	}
	data, err := json.Marshal(entry)
	if err != nil {
		return err
	}

	future := n.Raft.Apply(data, 10*time.Second)
	return future.Error()
}

// Join joins an existing cluster
func (n *Node) Join(nodeID, addr string) error {
	configFuture := n.Raft.GetConfiguration()
	if err := configFuture.Error(); err != nil {
		return err
	}

	for _, srv := range configFuture.Configuration().Servers {
		if srv.ID == raft.ServerID(nodeID) || srv.Address == raft.ServerAddress(addr) {
			if srv.Address == raft.ServerAddress(addr) && srv.ID == raft.ServerID(nodeID) {
				return nil // already joined
			}
			future := n.Raft.RemoveServer(srv.ID, 0, 0)
			if err := future.Error(); err != nil {
				return err
			}
		}
	}

	f := n.Raft.AddVoter(raft.ServerID(nodeID), raft.ServerAddress(addr), 0, 0)
	if err := f.Error(); err != nil {
		return err
	}
	return nil
}
