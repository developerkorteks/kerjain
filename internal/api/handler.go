package api

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/skip2/go-qrcode"

	"lokerwa/internal/hub"
	"lokerwa/internal/storage"
	"lokerwa/internal/whatsapp"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

type Handler struct {
	manager   *whatsapp.Manager
	hub       *hub.Hub
	store     *storage.Storage
	mu        sync.RWMutex
	lastQR    string
	lastQRPNG []byte
}

func New(manager *whatsapp.Manager, h *hub.Hub, store *storage.Storage) *Handler {
	handler := &Handler{manager: manager, hub: h, store: store}

	manager.OnQR = func(qrCode string) {
		png, err := qrcode.Encode(qrCode, qrcode.Medium, 256)
		if err != nil {
			return
		}
		handler.mu.Lock()
		handler.lastQR = qrCode
		handler.lastQRPNG = png
		handler.mu.Unlock()
		h.Broadcast("qr", map[string]string{
			"png": base64.StdEncoding.EncodeToString(png),
		})
	}

	manager.OnStateChange = func(s whatsapp.State) {
		if s != whatsapp.StateConnecting {
			handler.mu.Lock()
			handler.lastQR = ""
			handler.lastQRPNG = nil
			handler.mu.Unlock()
		}
		h.Broadcast("status", map[string]string{"state": string(s)})
	}

	manager.OnGroupMsg = func(msg whatsapp.GroupMessage) {
		h.Broadcast("message", msg)
	}

	manager.OnGroupsUpdate = func(groups []whatsapp.GroupInfo) {
		h.Broadcast("groups", groups)
	}

	return handler
}

func (h *Handler) Register(r *gin.Engine, mediaDir string) {
	api := r.Group("/api")
	api.GET("/status", h.getStatus)
	api.GET("/qr", h.getQR)
	api.GET("/groups", h.getGroups)
	api.POST("/groups/toggle", h.toggleGroup)
	api.POST("/groups/refresh", h.refreshGroups)
	api.POST("/logout", h.postLogout)
	api.POST("/connect", h.postConnect)
	api.GET("/jobs", h.listJobs)
	api.GET("/jobs/:id", h.getJob)
	api.PATCH("/jobs/:id", h.patchJob)
	api.DELETE("/jobs/:id", h.deleteJob)
	api.POST("/groups/:jid/fetch-history", h.fetchHistory)

	r.Static("/media", mediaDir)
	r.GET("/ws", h.wsHandler)
}

func (h *Handler) getStatus(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"state":   h.manager.GetState(),
		"clients": h.hub.Count(),
	})
}

func (h *Handler) getQR(c *gin.Context) {
	h.mu.RLock()
	png := h.lastQRPNG
	h.mu.RUnlock()
	if png == nil {
		c.JSON(http.StatusNoContent, nil)
		return
	}
	c.Data(http.StatusOK, "image/png", png)
}

func (h *Handler) postLogout(c *gin.Context) {
	if err := h.manager.Logout(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	h.mu.Lock()
	h.lastQR = ""
	h.lastQRPNG = nil
	h.mu.Unlock()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) postConnect(c *gin.Context) {
	if h.manager.IsConnected() {
		c.JSON(http.StatusOK, gin.H{"ok": true, "already": true})
		return
	}
	go func() {
		if err := h.manager.Start(context.Background()); err != nil {
			h.hub.Broadcast("error", map[string]string{"message": err.Error()})
		}
	}()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) getGroups(c *gin.Context) {
	c.JSON(http.StatusOK, h.manager.GetGroups())
}

func (h *Handler) refreshGroups(c *gin.Context) {
	if !h.manager.IsConnected() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not connected"})
		return
	}
	h.manager.RefreshGroups(context.Background())
	c.JSON(http.StatusOK, gin.H{"ok": true, "count": len(h.manager.GetGroups())})
}

func (h *Handler) listJobs(c *gin.Context) {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	f := storage.Filter{
		Group:   c.Query("group"),
		MsgType: c.Query("type"),
		Status:  c.Query("status"),
		Page:    page,
		Limit:   limit,
	}
	if h.store == nil {
		c.JSON(http.StatusOK, storage.Page{})
		return
	}
	result, err := h.store.List(f)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, result)
}

func (h *Handler) getJob(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	job, err := h.store.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) patchJob(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "no store"})
		return
	}
	var patch storage.JobPatch
	if err := c.ShouldBindJSON(&patch); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err := h.store.UpdateJob(c.Param("id"), patch); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	job, err := h.store.GetByID(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *Handler) deleteJob(c *gin.Context) {
	if h.store == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
		return
	}
	if err := h.store.Delete(c.Param("id")); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (h *Handler) toggleGroup(c *gin.Context) {
	var body struct {
		JID string `json:"jid"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.JID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "jid required"})
		return
	}
	enabled, err := h.manager.ToggleGroup(body.JID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	h.hub.Broadcast("groups", h.manager.GetGroups())
	c.JSON(http.StatusOK, gin.H{"jid": body.JID, "enabled": enabled})
}

// fetchHistory requests an on-demand history sync from WhatsApp for a specific group.
// Uses the oldest stored message for the group as the anchor. If none stored, sends a
// no-anchor request (WA will send its default recent batch).
func (h *Handler) fetchHistory(c *gin.Context) {
	if !h.manager.IsConnected() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "not connected"})
		return
	}
	jid := c.Param("jid")
	count, _ := strconv.Atoi(c.DefaultQuery("count", "100"))
	if count <= 0 || count > 1000 {
		count = 100
	}

	var msgID, senderJID string
	var fromMe bool
	var ts time.Time

	if h.store != nil {
		oldest, err := h.store.OldestForGroup(jid)
		if err == nil && oldest != nil {
			msgID = oldest.ID
			senderJID = oldest.SenderJID
			ts, _ = time.Parse(time.RFC3339, oldest.PostedAt)
		}
	}

	if err := h.manager.RequestGroupHistory(context.Background(), jid, msgID, senderJID, fromMe, ts, count); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"ok":      true,
		"group":   jid,
		"count":   count,
		"anchor":  msgID,
		"message": "history sync requested — new messages will appear in /api/jobs in ~30s",
	})
}

func (h *Handler) wsHandler(c *gin.Context) {
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return
	}

	// Send current state immediately to this new client
	state := h.manager.GetState()
	_ = conn.WriteJSON(hub.Message{Type: "status", Data: map[string]string{"state": string(state)}})

	// If QR is pending, send it directly so new clients don't miss it
	if state == whatsapp.StateConnecting {
		h.mu.RLock()
		png := h.lastQRPNG
		h.mu.RUnlock()
		if png != nil {
			_ = conn.WriteJSON(hub.Message{Type: "qr", Data: map[string]string{
				"png": base64.StdEncoding.EncodeToString(png),
			}})
		}
	}

	// Send current groups list
	if groups := h.manager.GetGroups(); len(groups) > 0 {
		_ = conn.WriteJSON(hub.Message{Type: "groups", Data: groups})
	}

	h.hub.Register(conn)

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			break
		}
	}
}
