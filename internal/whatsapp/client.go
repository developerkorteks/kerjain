package whatsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
)

const groupsStatePath = "./data/groups.json"

type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
)

type GroupInfo struct {
	JID     string `json:"jid"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type Manager struct {
	clientMu sync.RWMutex
	client   *whatsmeow.Client

	groupMu       sync.RWMutex
	enabledGroups map[string]bool
	joinedGroups  []GroupInfo

	OnQR           func(qrCode string)
	OnStateChange  func(s State)
	OnGroupMsg     func(msg GroupMessage)
	OnJobEvt       func(msg GroupMessage)
	OnGroupsUpdate func(groups []GroupInfo)
}

func NewManager() *Manager {
	m := &Manager{
		enabledGroups: make(map[string]bool),
	}
	if saved := loadGroupsState(); saved != nil {
		m.enabledGroups = saved
	}
	return m
}

func loadGroupsState() map[string]bool {
	data, err := os.ReadFile(groupsStatePath)
	if err != nil {
		return nil
	}
	var m map[string]bool
	if err := json.Unmarshal(data, &m); err != nil {
		return nil
	}
	return m
}

func saveGroupsState(m map[string]bool) {
	data, err := json.Marshal(m)
	if err != nil {
		return
	}
	if err := os.MkdirAll("./data", 0755); err != nil {
		return
	}
	_ = os.WriteFile(groupsStatePath, data, 0644)
}

func (m *Manager) Start(ctx context.Context) error {
	m.clientMu.Lock()
	defer m.clientMu.Unlock()

	container, err := initStore(ctx)
	if err != nil {
		return err
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}

	logger := waLog.Stdout("Client", "WARN", true)
	m.client = whatsmeow.NewClient(deviceStore, logger)

	m.client.AddEventHandler(m.handleEvent)

	if m.client.Store.ID == nil {
		ch, err := m.client.GetQRChannel(ctx)
		if err != nil {
			return fmt.Errorf("qr channel: %w", err)
		}

		if err := m.client.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}

		go func() {
			for evt := range ch {
				switch evt.Event {
				case "code":
					if m.OnQR != nil {
						m.OnQR(evt.Code)
					}
				case "success":
					if m.OnStateChange != nil {
						m.OnStateChange(StateConnected)
					}
				}
			}
		}()

		if m.OnStateChange != nil {
			m.OnStateChange(StateConnecting)
		}
	} else {
		if err := m.client.Connect(); err != nil {
			return fmt.Errorf("connect: %w", err)
		}
		if m.OnStateChange != nil {
			m.OnStateChange(StateConnected)
		}
	}

	return nil
}

func (m *Manager) Logout() error {
	m.clientMu.Lock()
	if m.client != nil {
		_ = m.client.Logout(context.Background())
		m.client.Disconnect()
		m.client = nil
	}
	m.clientMu.Unlock()

	if err := wipeStore(); err != nil {
		return err
	}

	m.groupMu.Lock()
	m.enabledGroups = make(map[string]bool)
	m.joinedGroups = nil
	m.groupMu.Unlock()

	if m.OnStateChange != nil {
		m.OnStateChange(StateDisconnected)
	}
	if m.OnGroupsUpdate != nil {
		m.OnGroupsUpdate([]GroupInfo{})
	}
	return nil
}

func (m *Manager) GetState() State {
	m.clientMu.RLock()
	defer m.clientMu.RUnlock()

	if m.client == nil {
		return StateDisconnected
	}
	if m.client.IsConnected() {
		return StateConnected
	}
	return StateConnecting
}

func (m *Manager) IsConnected() bool {
	return m.GetState() == StateConnected
}

func (m *Manager) GetGroups() []GroupInfo {
	m.groupMu.RLock()
	defer m.groupMu.RUnlock()
	out := make([]GroupInfo, len(m.joinedGroups))
	copy(out, m.joinedGroups)
	return out
}

func (m *Manager) ToggleGroup(jid string) (bool, error) {
	m.groupMu.Lock()
	defer m.groupMu.Unlock()
	for i := range m.joinedGroups {
		if m.joinedGroups[i].JID == jid {
			m.enabledGroups[jid] = !m.enabledGroups[jid]
			m.joinedGroups[i].Enabled = m.enabledGroups[jid]
			// persist immediately
			snap := make(map[string]bool, len(m.enabledGroups))
			for k, v := range m.enabledGroups {
				snap[k] = v
			}
			go saveGroupsState(snap)
			return m.joinedGroups[i].Enabled, nil
		}
	}
	return false, fmt.Errorf("group not found: %s", jid)
}

func (m *Manager) groupName(jid string) string {
	m.groupMu.RLock()
	defer m.groupMu.RUnlock()
	for _, g := range m.joinedGroups {
		if g.JID == jid {
			return g.Name
		}
	}
	return ""
}

// RefreshGroups is the public wrapper — callable from outside the package.
func (m *Manager) RefreshGroups(ctx context.Context) {
	m.fetchGroups(ctx)
}

func (m *Manager) fetchGroups(ctx context.Context) {
	m.clientMu.RLock()
	cli := m.client
	m.clientMu.RUnlock()
	if cli == nil {
		return
	}
	groups, err := cli.GetJoinedGroups(ctx)
	if err != nil {
		log.Printf("fetchGroups error: %v", err)
		return
	}
	m.groupMu.Lock()
	m.joinedGroups = make([]GroupInfo, len(groups))
	for i, g := range groups {
		jid := g.JID.String()
		m.joinedGroups[i] = GroupInfo{
			JID:     jid,
			Name:    g.GroupName.Name,
			Enabled: m.enabledGroups[jid],
		}
	}
	snap := make([]GroupInfo, len(m.joinedGroups))
	copy(snap, m.joinedGroups)
	m.groupMu.Unlock()
	if m.OnGroupsUpdate != nil {
		m.OnGroupsUpdate(snap)
	}
}

func (m *Manager) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.LoggedOut:
		_ = v
		go func() {
			_ = m.Logout()
		}()
	case *events.Connected:
		if m.OnStateChange != nil {
			m.OnStateChange(StateConnected)
		}
		go func() {
			ctx := context.Background()
			m.fetchGroups(ctx)
			// Retry once after 30s — fetchGroups often times out during initial history sync
			time.Sleep(30 * time.Second)
			if len(m.GetGroups()) == 0 {
				log.Printf("fetchGroups retry after initial sync...")
				m.fetchGroups(ctx)
			}
		}()
	case *events.Disconnected:
		if m.OnStateChange != nil {
			m.OnStateChange(StateDisconnected)
		}
	case *events.Message:
		m.processMessage(v)
	case *events.HistorySync:
		go m.processHistorySync(v)
	}
}
