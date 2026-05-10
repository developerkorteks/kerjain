package whatsapp

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// processHistorySync processes historical messages received from WhatsApp.
// Only messages from enabled groups are dispatched through OnJobEvt.
func (m *Manager) processHistorySync(evt *events.HistorySync) {
	syncType := evt.Data.GetSyncType().String()
	convs := evt.Data.GetConversations()
	log.Printf("[history] HistorySync received: type=%s conversations=%d", syncType, len(convs))

	total := 0
	for _, conv := range convs {
		jid := conv.GetID()
		msgCount := len(conv.GetMessages())
		log.Printf("[history]   conv jid=%s msgs=%d name=%q", jid, msgCount, conv.GetName())

		if !strings.HasSuffix(jid, "@g.us") {
			continue
		}
		m.groupMu.RLock()
		enabled := m.enabledGroups[jid]
		m.groupMu.RUnlock()
		if !enabled {
			log.Printf("[history]   SKIP (not enabled): %s", jid)
			continue
		}

		groupName := conv.GetName()
		if groupName == "" {
			groupName = m.groupName(jid)
		}

		count := 0
		for _, hm := range conv.GetMessages() {
			wmi := hm.GetMessage()
			if wmi == nil || wmi.GetMessage() == nil {
				continue
			}
			key := wmi.GetKey()
			if key == nil {
				continue
			}

			senderJID := key.GetParticipant()
			if senderJID == "" {
				senderJID = wmi.GetParticipant()
			}

			ts := time.Unix(int64(wmi.GetMessageTimestamp()), 0)

			msg := GroupMessage{
				MsgID:      key.GetID(),
				GroupJID:   jid,
				GroupName:  groupName,
				SenderJID:  senderJID,
				SenderName: wmi.GetPushName(),
				Timestamp:  ts,
			}

			inner := unwrapMsg(wmi.GetMessage())
			if !parseContent(inner, &msg) {
				continue
			}
			msg.Quoted = extractQuoted(inner)

			if msg.MsgType == "image" {
				if img := inner.GetImageMessage(); img != nil {
					innerRef := inner
					imgRef := img
					msg.DownloadFn = func() ([]byte, string, error) {
						m.clientMu.RLock()
						cli := m.client
						m.clientMu.RUnlock()
						if cli == nil {
							return nil, "", fmt.Errorf("client disconnected")
						}
						data, err := cli.DownloadAny(context.Background(), innerRef)
						return data, imgRef.GetMimetype(), err
					}
				}
			}

			if m.OnJobEvt != nil {
				m.OnJobEvt(msg)
			}
			count++
		}
		if count > 0 {
			log.Printf("[history] group=%s synced=%d msgs", jid, count)
			total += count
		}
	}
	if total > 0 {
		log.Printf("[history] total synced=%d messages", total)
	}
}

// RequestGroupHistory sends an on-demand history sync request to the primary device.
// anchor* params describe the oldest message we already have; WA will send older ones.
// If no anchor is known, pass empty msgID so WA sends the most recent batch.
func (m *Manager) RequestGroupHistory(ctx context.Context, groupJID, msgID, senderJID string, fromMe bool, ts time.Time, count int) error {
	m.clientMu.RLock()
	cli := m.client
	m.clientMu.RUnlock()
	if cli == nil {
		return fmt.Errorf("not connected")
	}
	if cli.Store.ID == nil {
		return fmt.Errorf("device not registered — scan QR first")
	}

	chatJID, err := types.ParseJID(groupJID)
	if err != nil {
		return fmt.Errorf("invalid group JID: %w", err)
	}

	var senderJ types.JID
	if senderJID != "" {
		senderJ, _ = types.ParseJID(senderJID)
	}

	msgInfo := &types.MessageInfo{
		MessageSource: types.MessageSource{
			Chat:     chatJID,
			Sender:   senderJ,
			IsFromMe: fromMe,
			IsGroup:  true,
		},
		ID:        types.MessageID(msgID),
		Timestamp: ts,
	}

	req := cli.BuildHistorySyncRequest(msgInfo, count)
	selfJID := cli.Store.ID.ToNonAD()
	if _, err = cli.SendMessage(ctx, selfJID, req); err != nil {
		return fmt.Errorf("send history sync request: %w", err)
	}
	log.Printf("[history] on-demand request sent: group=%s count=%d", groupJID, count)
	return nil
}
