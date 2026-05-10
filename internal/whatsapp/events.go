package whatsapp

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types/events"
)

type QuotedMsg struct {
	SenderJID string `json:"sender_jid"`
	MsgType   string `json:"msg_type"`
	Body      string `json:"body"`
}

type GroupMessage struct {
	MsgID      string     `json:"msg_id"`
	GroupJID   string     `json:"group_jid"`
	GroupName  string     `json:"group_name,omitempty"`
	SenderJID  string     `json:"sender_jid"`
	SenderName string     `json:"sender_name"`
	MsgType    string     `json:"msg_type"`
	Body       string     `json:"body,omitempty"`
	Caption    string     `json:"caption,omitempty"`
	FileName   string     `json:"file_name,omitempty"`
	Timestamp  time.Time  `json:"timestamp"`
	Quoted     *QuotedMsg `json:"quoted,omitempty"`

	// DownloadFn is set for media messages; not serialized to JSON.
	DownloadFn func() (data []byte, mime string, err error) `json:"-"`
}

func (m *Manager) processMessage(evt *events.Message) {
	if !evt.Info.IsGroup {
		return
	}

	groupJID := evt.Info.Chat.String()

	m.groupMu.RLock()
	enabled := m.enabledGroups[groupJID]
	m.groupMu.RUnlock()
	if !enabled {
		return
	}

	if evt.Message == nil {
		return
	}

	msg := GroupMessage{
		MsgID:      evt.Info.ID,
		GroupJID:   groupJID,
		GroupName:  m.groupName(groupJID),
		SenderJID:  evt.Info.Sender.String(),
		SenderName: evt.Info.PushName,
		Timestamp:  evt.Info.Timestamp,
	}

	inner := unwrapMsg(evt.Message)
	if !parseContent(inner, &msg) {
		return
	}
	msg.Quoted = extractQuoted(inner)

	// Set download function for image messages
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

	if m.OnGroupMsg != nil {
		m.OnGroupMsg(msg)
	}
	if m.OnJobEvt != nil {
		m.OnJobEvt(msg)
	}
}

// unwrapMsg unwraps ephemeral/viewonce/documentWithCaption layers.
func unwrapMsg(m *waE2E.Message) *waE2E.Message {
	if e := m.GetEphemeralMessage(); e != nil && e.Message != nil {
		return unwrapMsg(e.Message)
	}
	if v := m.GetViewOnceMessage(); v != nil && v.Message != nil {
		return unwrapMsg(v.Message)
	}
	if v := m.GetViewOnceMessageV2(); v != nil && v.Message != nil {
		return unwrapMsg(v.Message)
	}
	if d := m.GetDocumentWithCaptionMessage(); d != nil && d.Message != nil {
		return unwrapMsg(d.Message)
	}
	return m
}

// parseContent fills msg fields based on message type. Returns false if unknown/skip.
func parseContent(m *waE2E.Message, msg *GroupMessage) bool {
	// Plain text
	if text := m.GetConversation(); text != "" {
		msg.MsgType = "text"
		msg.Body = text
		return true
	}
	// Extended text (links, mentions, quoted reply)
	if ext := m.GetExtendedTextMessage(); ext != nil {
		msg.MsgType = "text"
		msg.Body = ext.GetText()
		return true
	}
	// Image
	if img := m.GetImageMessage(); img != nil {
		msg.MsgType = "image"
		msg.Caption = img.GetCaption()
		return true
	}
	// Video
	if vid := m.GetVideoMessage(); vid != nil {
		msg.MsgType = "video"
		msg.Caption = vid.GetCaption()
		return true
	}
	// Audio / PTT
	if aud := m.GetAudioMessage(); aud != nil {
		if aud.GetPTT() {
			msg.MsgType = "ptt"
		} else {
			msg.MsgType = "audio"
		}
		return true
	}
	// Document
	if doc := m.GetDocumentMessage(); doc != nil {
		msg.MsgType = "document"
		msg.FileName = doc.GetFileName()
		msg.Caption = doc.GetCaption()
		return true
	}
	// Sticker
	if m.GetStickerMessage() != nil {
		msg.MsgType = "sticker"
		return true
	}
	// Location
	if loc := m.GetLocationMessage(); loc != nil {
		msg.MsgType = "location"
		name := loc.GetName()
		if name == "" {
			name = loc.GetAddress()
		}
		if name == "" {
			name = fmt.Sprintf("%.5f, %.5f", loc.GetDegreesLatitude(), loc.GetDegreesLongitude())
		}
		msg.Body = name
		return true
	}
	// Live location
	if ll := m.GetLiveLocationMessage(); ll != nil {
		msg.MsgType = "live_location"
		msg.Body = ll.GetCaption()
		return true
	}
	// Contact
	if contact := m.GetContactMessage(); contact != nil {
		msg.MsgType = "contact"
		msg.Body = contact.GetDisplayName()
		return true
	}
	// Multiple contacts
	if contacts := m.GetContactsArrayMessage(); contacts != nil {
		msg.MsgType = "contacts"
		msg.Body = fmt.Sprintf("%d contacts", len(contacts.GetContacts()))
		return true
	}
	// Reaction (emoji on another message)
	if r := m.GetReactionMessage(); r != nil {
		msg.MsgType = "reaction"
		msg.Body = r.GetText()
		if msg.Body == "" {
			msg.Body = "(removed reaction)"
		}
		return true
	}
	// Poll
	if poll := m.GetPollCreationMessage(); poll != nil {
		msg.MsgType = "poll"
		msg.Body = poll.GetName()
		return true
	}
	if m.GetPollUpdateMessage() != nil {
		msg.MsgType = "poll_vote"
		return true
	}
	// Group invite link
	if inv := m.GetGroupInviteMessage(); inv != nil {
		msg.MsgType = "group_invite"
		msg.Body = inv.GetGroupName()
		return true
	}
	// Protocol: revoke (delete) or edit
	if proto := m.GetProtocolMessage(); proto != nil {
		switch proto.GetType() {
		case waE2E.ProtocolMessage_REVOKE:
			msg.MsgType = "revoked"
			return true
		case waE2E.ProtocolMessage_MESSAGE_EDIT:
			msg.MsgType = "edited"
			if edited := proto.GetEditedMessage(); edited != nil {
				parseContent(unwrapMsg(edited), msg)
			}
			return true
		}
	}
	return false
}

// extractQuoted extracts the quoted/replied-to message from ExtendedTextMessage context.
func extractQuoted(m *waE2E.Message) *QuotedMsg {
	ext := m.GetExtendedTextMessage()
	if ext == nil {
		return nil
	}
	ctx := ext.GetContextInfo()
	if ctx == nil {
		return nil
	}
	quoted := ctx.GetQuotedMessage()
	if quoted == nil {
		return nil
	}
	q := &QuotedMsg{SenderJID: ctx.GetParticipant()}
	tmp := &GroupMessage{}
	if parseContent(quoted, tmp) {
		q.MsgType = tmp.MsgType
		q.Body = tmp.Body
		if q.Body == "" {
			q.Body = tmp.Caption
		}
	}
	return q
}
